package imapclient_test

import (
	"bufio"
	"io"
	"net"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// scriptStep is one command the scripted server expects, without its tag,
// and the reply it sends. Every "TAG" in reply becomes the command's tag.
type scriptStep struct {
	want  string
	reply string
}

// newScriptedClient connects a client to a server that plays steps in order.
// Commands are compared byte for byte, so these tests pin the wire format.
func newScriptedClient(t *testing.T, steps ...scriptStep) *imapclient.Client {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer serverConn.Close()
		r := bufio.NewReader(serverConn)
		if _, err := io.WriteString(serverConn, "* OK [CAPABILITY IMAP4rev1 LITERAL+ X-GM-EXT-1 PREVIEW] ready\r\n"); err != nil {
			t.Errorf("writing greeting: %v", err)
			return
		}
		for _, step := range steps {
			cmd, err := readScriptedCommand(r)
			if err != nil {
				t.Errorf("reading command: %v", err)
				return
			}
			tag, rest, _ := strings.Cut(cmd, " ")
			if rest != step.want {
				t.Errorf("command = %q, want %q", rest, step.want)
				return
			}
			if _, err := io.WriteString(serverConn, strings.ReplaceAll(step.reply, "TAG", tag)); err != nil {
				t.Errorf("writing reply: %v", err)
				return
			}
		}
	}()
	client := imapclient.New(clientConn, nil)
	t.Cleanup(func() {
		client.Close()
		<-done
	})
	if err := client.WaitGreeting(); err != nil {
		t.Fatalf("WaitGreeting() = %v", err)
	}
	return client
}

// readScriptedCommand reads one command line, following non-synchronizing
// literals ({n+}) so a command that carries one comes back whole.
func readScriptedCommand(r *bufio.Reader) (string, error) {
	var b strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return "", err
		}
		line = strings.TrimSuffix(line, "\r\n")
		b.WriteString(line)
		open := strings.LastIndexByte(line, '{')
		if open < 0 || !strings.HasSuffix(line, "+}") {
			return b.String(), nil
		}
		n, err := strconv.Atoi(line[open+1 : len(line)-2])
		if err != nil {
			return b.String(), nil
		}
		lit := make([]byte, n)
		if _, err := io.ReadFull(r, lit); err != nil {
			return "", err
		}
		b.WriteString("\r\n")
		b.Write(lit)
	}
}

const (
	testGmailMsgID    = 1278455344230334865
	testGmailThreadID = 1266894439832287888
)

func TestSearchGmailRaw(t *testing.T) {
	client := newScriptedClient(t, scriptStep{
		want:  `UID SEARCH X-GM-RAW "from:alex has:attachment"`,
		reply: "* SEARCH 4 9\r\nTAG OK SEARCH completed\r\n",
	})
	data, err := client.UIDSearch(&imap.SearchCriteria{GmailRaw: []string{"from:alex has:attachment"}}, nil).Wait()
	if err != nil {
		t.Fatalf("UIDSearch().Wait() = %v", err)
	}
	if got, want := data.AllUIDs(), []imap.UID{4, 9}; !reflect.DeepEqual(got, want) {
		t.Errorf("AllUIDs() = %v, want %v", got, want)
	}
}

func TestSearchGmailIDs(t *testing.T) {
	client := newScriptedClient(t, scriptStep{
		want:  "UID SEARCH UID 1:41 X-GM-MSGID 1278455344230334865 X-GM-THRID 1266894439832287888",
		reply: "* SEARCH 7\r\nTAG OK SEARCH completed\r\n",
	})
	var uids imap.UIDSet
	uids.AddRange(1, 41)
	criteria := &imap.SearchCriteria{
		UID:           []imap.UIDSet{uids},
		GmailMsgID:    []uint64{testGmailMsgID},
		GmailThreadID: []uint64{testGmailThreadID},
	}
	data, err := client.UIDSearch(criteria, nil).Wait()
	if err != nil {
		t.Fatalf("UIDSearch().Wait() = %v", err)
	}
	if got, want := data.AllUIDs(), []imap.UID{7}; !reflect.DeepEqual(got, want) {
		t.Errorf("AllUIDs() = %v, want %v", got, want)
	}
}

func TestSearchGmailRawUTF8(t *testing.T) {
	client := newScriptedClient(t, scriptStep{
		want:  "UID SEARCH CHARSET UTF-8 X-GM-RAW {5+}\r\ncafé",
		reply: "* SEARCH\r\nTAG OK SEARCH completed\r\n",
	})
	if _, err := client.UIDSearch(&imap.SearchCriteria{GmailRaw: []string{"café"}}, nil).Wait(); err != nil {
		t.Fatalf("UIDSearch().Wait() = %v", err)
	}
}

func TestFetchGmail(t *testing.T) {
	client := newScriptedClient(t, scriptStep{
		want: "UID FETCH 7 (UID X-GM-MSGID X-GM-THRID X-GM-LABELS)",
		reply: `* 1 FETCH (UID 7 X-GM-MSGID 1278455344230334865 X-GM-THRID 1266894439832287888 X-GM-LABELS (\Inbox "\\Important" "Work/Q3" "&AMk-t&AOk-"))` + "\r\n" +
			"TAG OK FETCH completed\r\n",
	})
	options := &imap.FetchOptions{GmailMsgID: true, GmailThreadID: true, GmailLabels: true}
	msgs, err := client.Fetch(imap.UIDSetNum(7), options).Collect()
	if err != nil {
		t.Fatalf("Fetch().Collect() = %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	m := msgs[0]
	if m.UID != 7 || m.GmailMsgID != testGmailMsgID || m.GmailThreadID != testGmailThreadID {
		t.Errorf("UID, X-GM-MSGID, X-GM-THRID = %d, %d, %d", m.UID, m.GmailMsgID, m.GmailThreadID)
	}
	if want := []string{`\Inbox`, `\Important`, "Work/Q3", "Été"}; !reflect.DeepEqual(m.GmailLabels, want) {
		t.Errorf("GmailLabels = %q, want %q", m.GmailLabels, want)
	}
}

func TestFetchPreview(t *testing.T) {
	client := newScriptedClient(t,
		scriptStep{
			want:  "UID FETCH 7 (UID PREVIEW)",
			reply: "* 1 FETCH (UID 7 PREVIEW \"Are we still on for Thursday?\")\r\nTAG OK FETCH completed\r\n",
		},
		scriptStep{
			want:  "UID FETCH 8 (UID PREVIEW)",
			reply: "* 2 FETCH (UID 8 PREVIEW NIL)\r\nTAG OK FETCH completed\r\n",
		},
	)
	msgs, err := client.Fetch(imap.UIDSetNum(7), &imap.FetchOptions{Preview: true}).Collect()
	if err != nil {
		t.Fatalf("Fetch(7).Collect() = %v", err)
	}
	if len(msgs) != 1 || msgs[0].Preview == nil || *msgs[0].Preview != "Are we still on for Thursday?" {
		t.Errorf("preview of 7 = %#v", msgs)
	}
	msgs, err = client.Fetch(imap.UIDSetNum(8), &imap.FetchOptions{Preview: true}).Collect()
	if err != nil {
		t.Fatalf("Fetch(8).Collect() = %v", err)
	}
	if len(msgs) != 1 || msgs[0].Preview != nil {
		t.Errorf("preview of 8 = %#v, want nil for NIL", msgs)
	}
}
