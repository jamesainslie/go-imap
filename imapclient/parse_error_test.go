package imapclient_test

import (
	"errors"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// A response the client cannot parse fails the command with a ParseError, so
// a caller can tell bad content from a broken connection.
func TestFetchMalformedResponseIsParseError(t *testing.T) {
	client := newScriptedClient(t, scriptStep{
		want: "UID FETCH 7 (UID BODYSTRUCTURE)",
		// A body type without its subtype.
		reply: `* 1 FETCH (UID 7 BODYSTRUCTURE ("TEXT" NIL))` + "\r\n" +
			"TAG OK FETCH completed\r\n",
	})
	options := &imap.FetchOptions{UID: true, BodyStructure: &imap.FetchItemBodyStructure{Extended: true}}
	_, err := client.Fetch(imap.UIDSetNum(7), options).Collect()
	var pe *imapclient.ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("Fetch().Collect() = %v, want a *ParseError", err)
	}
	if pe.Err == nil || pe.Error() == "" {
		t.Errorf("ParseError = %#v, want the parse failure inside", pe)
	}
}

// A connection that ends in the middle of a response is not a ParseError.
func TestFetchDroppedConnectionIsNotParseError(t *testing.T) {
	client := newScriptedClient(t, scriptStep{
		want:  "UID FETCH 7 (UID BODYSTRUCTURE)",
		reply: `* 1 FETCH (UID 7 BODYSTRUCTURE ("TEXT" "PLAIN" `,
	})
	options := &imap.FetchOptions{UID: true, BodyStructure: &imap.FetchItemBodyStructure{Extended: true}}
	_, err := client.Fetch(imap.UIDSetNum(7), options).Collect()
	if err == nil {
		t.Fatal("Fetch().Collect() = nil, want an error")
	}
	var pe *imapclient.ParseError
	if errors.As(err, &pe) {
		t.Errorf("Fetch().Collect() = %v, a ParseError, want a connection error", err)
	}
}
