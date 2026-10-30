package internal

import (
	"bufio"
	"bytes"
	"testing"

	"github.com/emersion/go-imap/v2/internal/imapwire"
)

func TestEncodeGmailLabel(t *testing.T) {
	tests := []struct {
		label string
		want  string
	}{
		{`\Inbox`, `\Inbox`},
		{`\Important`, `\Important`},
		{`Work`, `"Work"`},
		{`Work Stuff`, `"Work Stuff"`},
		{`say "hi"`, `"say \"hi\""`},
		{`R&D`, `"R&-D"`},
		{`\Project Stuff`, `"\\Project Stuff"`},
		{`\Inbox)`, `"\\Inbox)"`},
		{`\`, `"\\"`},
		{`\Ünïcode`, `"\\&ANw-n&AO8-code"`},
		// CR and LF are encoded in modified UTF-7, so the label stays one token.
		{"\\Inbox)\r\nx NOOP\r\ny NOOP (", `"\\Inbox)&AA0ACg-x NOOP&AA0ACg-y NOOP ("`},
	}
	for _, tt := range tests {
		var buf bytes.Buffer
		bw := bufio.NewWriter(&buf)
		enc := imapwire.NewEncoder(bw, imapwire.ConnSideClient)
		enc.LiteralPlus = true
		EncodeGmailLabel(enc, tt.label)
		if err := enc.CRLF(); err != nil {
			t.Fatalf("EncodeGmailLabel(%q): %v", tt.label, err)
		}
		if got := buf.String(); got != tt.want+"\r\n" {
			t.Errorf("EncodeGmailLabel(%q) = %q, want %q", tt.label, got, tt.want)
		}
	}
}
