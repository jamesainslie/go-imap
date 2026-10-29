package imapserver_test

import (
	"net"
	"reflect"
	"sync"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

const (
	testGmailMsgID    = 1278455344230334865
	testGmailThreadID = 1266894439832287888
)

// gmailSession answers the Gmail extensions with fixed data and records what
// the client asked for. Everything else goes to imapmemserver.
type gmailSession struct {
	imapserver.Session

	mu     sync.Mutex
	search *imap.SearchCriteria
	fetch  *imap.FetchOptions
}

func (s *gmailSession) Search(_ imapserver.NumKind, criteria *imap.SearchCriteria, _ *imap.SearchOptions) (*imap.SearchData, error) {
	s.mu.Lock()
	s.search = criteria
	s.mu.Unlock()
	return &imap.SearchData{All: imap.UIDSetNum(4, 9)}, nil
}

func (s *gmailSession) Fetch(w *imapserver.FetchWriter, _ imap.NumSet, options *imap.FetchOptions) error {
	s.mu.Lock()
	s.fetch = options
	s.mu.Unlock()
	m := w.CreateMessage(1)
	m.WriteUID(7)
	if options.GmailMsgID {
		m.WriteGmailMsgID(testGmailMsgID)
	}
	if options.GmailThreadID {
		m.WriteGmailThreadID(testGmailThreadID)
	}
	if options.GmailLabels {
		m.WriteGmailLabels([]string{`\Inbox`, `\Important`, "Work/Q3", "Été"})
	}
	if options.Preview {
		preview := "Are we still on for Thursday?"
		m.WritePreview(&preview)
	}
	return m.Close()
}

func (s *gmailSession) lastSearch() *imap.SearchCriteria {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.search
}

func newGmailPair(t *testing.T) (*imapclient.Client, *gmailSession) {
	t.Helper()
	mem := imapmemserver.New()
	user := imapmemserver.NewUser("user", "pass")
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	mem.AddUser(user)
	sess := &gmailSession{Session: mem.NewSession()}
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return sess, nil, nil
		},
		Caps:         imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapGmailExt1: {}, imap.CapPreview: {}},
		InsecureAuth: true,
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	client, err := imapclient.DialInsecure(ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	if err := client.Login("user", "pass").Wait(); err != nil {
		t.Fatalf("Login() = %v", err)
	}
	if _, err := client.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		t.Fatalf("Select() = %v", err)
	}
	return client, sess
}

func TestServerAdvertisesGmailAndPreview(t *testing.T) {
	client, _ := newGmailPair(t)
	caps := client.Caps()
	if !caps.Has(imap.CapGmailExt1) || !caps.Has(imap.CapPreview) {
		t.Errorf("Caps() = %v, want X-GM-EXT-1 and PREVIEW", caps)
	}
}

func TestServerSearchGmail(t *testing.T) {
	client, sess := newGmailPair(t)
	criteria := &imap.SearchCriteria{
		GmailRaw:      []string{"from:alex has:attachment"},
		GmailMsgID:    []uint64{testGmailMsgID},
		GmailThreadID: []uint64{testGmailThreadID},
	}
	data, err := client.UIDSearch(criteria, nil).Wait()
	if err != nil {
		t.Fatalf("UIDSearch() = %v", err)
	}
	if got, want := data.AllUIDs(), []imap.UID{4, 9}; !reflect.DeepEqual(got, want) {
		t.Errorf("AllUIDs() = %v, want %v", got, want)
	}
	got := sess.lastSearch()
	if !reflect.DeepEqual(got.GmailRaw, criteria.GmailRaw) || !reflect.DeepEqual(got.GmailMsgID, criteria.GmailMsgID) ||
		!reflect.DeepEqual(got.GmailThreadID, criteria.GmailThreadID) {
		t.Errorf("server parsed %+v, want %+v", got, criteria)
	}
}

func TestServerSearchGmailRawUTF8(t *testing.T) {
	client, sess := newGmailPair(t)
	if _, err := client.UIDSearch(&imap.SearchCriteria{GmailRaw: []string{"café"}}, nil).Wait(); err != nil {
		t.Fatalf("UIDSearch() = %v", err)
	}
	if got := sess.lastSearch().GmailRaw; !reflect.DeepEqual(got, []string{"café"}) {
		t.Errorf("server parsed X-GM-RAW %q", got)
	}
}

func TestServerFetchGmailAndPreview(t *testing.T) {
	client, _ := newGmailPair(t)
	options := &imap.FetchOptions{GmailMsgID: true, GmailThreadID: true, GmailLabels: true, Preview: true}
	msgs, err := client.Fetch(imap.UIDSetNum(7), options).Collect()
	if err != nil {
		t.Fatalf("Fetch().Collect() = %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	m := msgs[0]
	if m.GmailMsgID != testGmailMsgID || m.GmailThreadID != testGmailThreadID {
		t.Errorf("ids = %d, %d", m.GmailMsgID, m.GmailThreadID)
	}
	if want := []string{`\Inbox`, `\Important`, "Work/Q3", "Été"}; !reflect.DeepEqual(m.GmailLabels, want) {
		t.Errorf("labels = %q, want %q", m.GmailLabels, want)
	}
	if m.Preview == nil || *m.Preview != "Are we still on for Thursday?" {
		t.Errorf("preview = %v", m.Preview)
	}
}
