package imapserver_test

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

// gmailStoreSession implements SessionGmail. It records the last X-GM-LABELS
// STORE and answers with the labels it was given. Everything else goes to
// imapmemserver.
type gmailStoreSession struct {
	imapserver.Session

	mu     sync.Mutex
	numSet imap.NumSet
	store  *imap.StoreGmailLabels
}

var _ imapserver.SessionGmail = (*gmailStoreSession)(nil)

func (s *gmailStoreSession) StoreGmailLabels(w *imapserver.FetchWriter, numSet imap.NumSet, store *imap.StoreGmailLabels) error {
	s.mu.Lock()
	s.numSet = numSet
	s.store = store
	s.mu.Unlock()
	if store.Silent {
		return nil
	}
	m := w.CreateMessage(1)
	if _, ok := numSet.(imap.UIDSet); ok {
		m.WriteUID(7)
	}
	m.WriteGmailLabels(store.Labels)
	return m.Close()
}

func (s *gmailStoreSession) last() (imap.NumSet, *imap.StoreGmailLabels) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.numSet, s.store
}

// startStoreServer serves an imapmemserver user with one message in INBOX.
// wrap turns each memory session into the session the server uses.
func startStoreServer(t *testing.T, wrap func(imapserver.Session) imapserver.Session) string {
	t.Helper()
	mem := imapmemserver.New()
	user := imapmemserver.NewUser("user", "pass")
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	msg := "From: alex@example.org\r\nSubject: hi\r\n\r\nhello\r\n"
	if _, err := user.Append("INBOX", strings.NewReader(msg), &imap.AppendOptions{}); err != nil {
		t.Fatal(err)
	}
	mem.AddUser(user)
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return wrap(mem.NewSession()), nil, nil
		},
		Caps:         imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapGmailExt1: {}},
		InsecureAuth: true,
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().String()
}

func dialSelected(t *testing.T, addr string) *imapclient.Client {
	t.Helper()
	client, err := imapclient.DialInsecure(addr, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	if err := client.Login("user", "pass").Wait(); err != nil {
		t.Fatalf("Login() = %v", err)
	}
	if _, err := client.Select("INBOX", nil).Wait(); err != nil {
		t.Fatalf("Select() = %v", err)
	}
	return client
}

func newGmailStorePair(t *testing.T) (*imapclient.Client, *gmailStoreSession) {
	t.Helper()
	sess := &gmailStoreSession{}
	addr := startStoreServer(t, func(mem imapserver.Session) imapserver.Session {
		sess.Session = mem
		return sess
	})
	return dialSelected(t, addr), sess
}

func TestServerStoreGmailLabels(t *testing.T) {
	client, sess := newGmailStorePair(t)
	labels := []string{`\Important`, "Work Stuff", "Work/Q3", "Été", "INBOX"}
	store := &imap.StoreGmailLabels{Op: imap.StoreFlagsAdd, Labels: labels}
	msgs, err := client.StoreGmailLabels(imap.UIDSetNum(7), store, nil).Collect()
	if err != nil {
		t.Fatalf("StoreGmailLabels().Collect() = %v", err)
	}
	numSet, got := sess.last()
	if !reflect.DeepEqual(got, store) {
		t.Errorf("server parsed %+v, want %+v", got, store)
	}
	if want := imap.UIDSetNum(7); !reflect.DeepEqual(numSet, want) {
		t.Errorf("numSet = %#v, want %#v", numSet, want)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if msgs[0].UID != 7 || !reflect.DeepEqual(msgs[0].GmailLabels, labels) {
		t.Errorf("UID, GmailLabels = %d, %q, want 7, %q", msgs[0].UID, msgs[0].GmailLabels, labels)
	}
}

func TestServerStoreGmailLabelsSilentSeqNum(t *testing.T) {
	client, sess := newGmailStorePair(t)
	store := &imap.StoreGmailLabels{Op: imap.StoreFlagsDel, Silent: true, Labels: []string{`\Inbox`}}
	msgs, err := client.StoreGmailLabels(imap.SeqSetNum(1), store, nil).Collect()
	if err != nil {
		t.Fatalf("StoreGmailLabels().Collect() = %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("got %d messages, want 0", len(msgs))
	}
	numSet, got := sess.last()
	if !reflect.DeepEqual(got, store) {
		t.Errorf("server parsed %+v, want %+v", got, store)
	}
	if want := imap.SeqSetNum(1); !reflect.DeepEqual(numSet, want) {
		t.Errorf("numSet = %#v, want %#v", numSet, want)
	}
}

// TestServerStoreGmailLabelsWire sends forms a Gmail client may use but
// imapclient never writes: quoted system labels, atoms and a bare label.
func TestServerStoreGmailLabelsWire(t *testing.T) {
	tests := []struct {
		cmd  string
		want *imap.StoreGmailLabels
	}{
		{
			cmd:  `UID STORE 7 X-GM-LABELS ("\\Important" "Work Stuff" Work &AMk-t&AOk- inbox)`,
			want: &imap.StoreGmailLabels{Op: imap.StoreFlagsSet, Labels: []string{`\Important`, "Work Stuff", "Work", "Été", "INBOX"}},
		},
		{
			cmd:  `UID STORE 7 +x-gm-labels.silent \Starred`,
			want: &imap.StoreGmailLabels{Op: imap.StoreFlagsAdd, Silent: true, Labels: []string{`\Starred`}},
		},
		{
			cmd:  `UID STORE 7 -X-GM-LABELS ()`,
			want: &imap.StoreGmailLabels{Op: imap.StoreFlagsDel},
		},
	}
	sess := &gmailStoreSession{}
	addr := startStoreServer(t, func(mem imapserver.Session) imapserver.Session {
		sess.Session = mem
		return sess
	})
	conn := dialRaw(t, addr)
	for i, tt := range tests {
		resp := conn.command(t, fmt.Sprintf("s%d", i), tt.cmd)
		if !strings.HasPrefix(resp, fmt.Sprintf("s%d OK", i)) {
			t.Errorf("%s: response = %q, want OK", tt.cmd, resp)
			continue
		}
		if _, got := sess.last(); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: server parsed %+v, want %+v", tt.cmd, got, tt.want)
		}
	}
}

func TestServerStoreGmailLabelsUnsupported(t *testing.T) {
	addr := startStoreServer(t, func(mem imapserver.Session) imapserver.Session { return mem })
	client := dialSelected(t, addr)
	store := &imap.StoreGmailLabels{Op: imap.StoreFlagsAdd, Labels: []string{"Work"}}
	_, err := client.StoreGmailLabels(imap.SeqSetNum(1), store, nil).Collect()
	var imapErr *imap.Error
	if !errors.As(err, &imapErr) || imapErr.Type != imap.StatusResponseTypeBad {
		t.Errorf("StoreGmailLabels() = %v, want BAD", err)
	}
}

func TestServerStoreUnknownItem(t *testing.T) {
	addr := startStoreServer(t, func(mem imapserver.Session) imapserver.Session {
		return &gmailStoreSession{Session: mem}
	})
	conn := dialRaw(t, addr)
	if resp := conn.command(t, "u1", `STORE 1 +X-GM-THRID (1)`); !strings.HasPrefix(resp, "u1 BAD") {
		t.Errorf("response = %q, want BAD", resp)
	}
}

func TestServerStoreFlagsWithGmailSession(t *testing.T) {
	client, sess := newGmailStorePair(t)
	store := &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagSeen, imap.FlagFlagged}}
	msgs, err := client.Store(imap.SeqSetNum(1), store, nil).Collect()
	if err != nil {
		t.Fatalf("Store().Collect() = %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if !hasFlag(msgs[0].Flags, imap.FlagSeen) || !hasFlag(msgs[0].Flags, imap.FlagFlagged) {
		t.Errorf("Flags = %v, want \\Seen and \\Flagged", msgs[0].Flags)
	}
	if _, got := sess.last(); got != nil {
		t.Errorf("FLAGS STORE reached StoreGmailLabels with %+v", got)
	}
}

func hasFlag(flags []imap.Flag, want imap.Flag) bool {
	for _, f := range flags {
		if f == want {
			return true
		}
	}
	return false
}

// rawConn is a logged in, selected connection that sends commands verbatim.
type rawConn struct {
	conn net.Conn
	r    *bufio.Reader
}

func dialRaw(t *testing.T, addr string) *rawConn {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	c := &rawConn{conn: conn, r: bufio.NewReader(conn)}
	if _, err := c.r.ReadString('\n'); err != nil {
		t.Fatalf("reading greeting: %v", err)
	}
	for _, cmd := range []string{"LOGIN user pass", "SELECT INBOX"} {
		if resp := c.command(t, "x", cmd); !strings.HasPrefix(resp, "x OK") {
			t.Fatalf("%s: response = %q", cmd, resp)
		}
	}
	return c
}

// command sends one command and returns its tagged response line.
func (c *rawConn) command(t *testing.T, tag, cmd string) string {
	t.Helper()
	if _, err := fmt.Fprintf(c.conn, "%s %s\r\n", tag, cmd); err != nil {
		t.Fatalf("writing %q: %v", cmd, err)
	}
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			t.Fatalf("reading response to %q: %v", cmd, err)
		}
		if strings.HasPrefix(line, tag+" ") {
			return strings.TrimSuffix(line, "\r\n")
		}
	}
}
