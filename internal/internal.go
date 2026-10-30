package internal

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/internal/imapwire"
)

const (
	DateTimeLayout = "_2-Jan-2006 15:04:05 -0700"
	DateLayout     = "2-Jan-2006"
)

const FlagRecent imap.Flag = "\\Recent" // removed in IMAP4rev2

func DecodeDateTime(dec *imapwire.Decoder) (time.Time, error) {
	var s string
	if !dec.Quoted(&s) {
		return time.Time{}, nil
	}
	t, err := time.Parse(DateTimeLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("in date-time: %v", err) // TODO: use imapwire.DecodeExpectError?
	}
	return t, err
}

func ExpectDateTime(dec *imapwire.Decoder) (time.Time, error) {
	t, err := DecodeDateTime(dec)
	if err != nil {
		return t, err
	}
	if !dec.Expect(!t.IsZero(), "date-time") {
		return t, dec.Err()
	}
	return t, nil
}

func ExpectDate(dec *imapwire.Decoder) (time.Time, error) {
	var s string
	if !dec.ExpectAString(&s) {
		return time.Time{}, dec.Err()
	}
	t, err := time.Parse(DateLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("in date: %v", err) // use imapwire.DecodeExpectError?
	}
	return t, nil
}

func ExpectFlagList(dec *imapwire.Decoder) ([]imap.Flag, error) {
	var flags []imap.Flag
	err := dec.ExpectList(func() error {
		// Some servers start the list with a space, so we need to skip it
		// https://github.com/emersion/go-imap/pull/633
		dec.SP()

		flag, err := ExpectFlag(dec)
		if err != nil {
			return err
		}
		flags = append(flags, flag)
		return nil
	})
	return flags, err
}

func ExpectCap(dec *imapwire.Decoder) (imap.Cap, error) {
	var name string
	if !dec.ExpectAtom(&name) {
		return "", dec.Err()
	}
	return canonicalCap(name), nil
}

func ExpectFlag(dec *imapwire.Decoder) (imap.Flag, error) {
	isSystem := dec.Special('\\')
	if isSystem && dec.Special('*') {
		return imap.FlagWildcard, nil // flag-perm
	}
	var name string
	if !dec.ExpectAtom(&name) {
		return "", fmt.Errorf("in flag: %w", dec.Err())
	}
	if isSystem {
		name = "\\" + name
	}
	return canonicalFlag(name), nil
}

// ExpectGmailLabel reads one Gmail label. System labels are atoms that start
// with a backslash. The rest are astrings in modified UTF-7, like mailbox
// names.
func ExpectGmailLabel(dec *imapwire.Decoder) (string, error) {
	var label string
	if dec.Special('\\') {
		if !dec.ExpectAtom(&label) {
			return "", fmt.Errorf("in Gmail label: %w", dec.Err())
		}
		return `\` + label, nil
	}
	if !dec.ExpectMailbox(&label) {
		return "", fmt.Errorf("in Gmail label: %w", dec.Err())
	}
	return label, nil
}

// EncodeGmailLabel writes one Gmail label. A system label, a backslash
// followed by ASCII atom characters, is written as an atom. Anything else,
// including a backslash label that is not a valid atom, is written like a
// mailbox name, quoted or as a literal, so a label can never end its token
// or the command early.
func EncodeGmailLabel(enc *imapwire.Encoder, label string) {
	if isSystemGmailLabel(label) {
		enc.Atom(label)
	} else {
		enc.Mailbox(label)
	}
}

// isSystemGmailLabel reports whether label is a backslash and one or more
// ASCII atom characters, the form Gmail's system labels take.
func isSystemGmailLabel(label string) bool {
	if len(label) < 2 || label[0] != '\\' {
		return false
	}
	for i := 1; i < len(label); i++ {
		if ch := label[i]; ch >= 0x80 || !imapwire.IsAtomChar(ch) {
			return false
		}
	}
	return true
}

func ExpectMailboxAttrList(dec *imapwire.Decoder) ([]imap.MailboxAttr, error) {
	var attrs []imap.MailboxAttr
	err := dec.ExpectList(func() error {
		attr, err := ExpectMailboxAttr(dec)
		if err != nil {
			return err
		}
		attrs = append(attrs, attr)
		return nil
	})
	return attrs, err
}

func ExpectMailboxAttr(dec *imapwire.Decoder) (imap.MailboxAttr, error) {
	flag, err := ExpectFlag(dec)
	return canonicalMailboxAttr(string(flag)), err
}

var (
	canonOnce        sync.Once
	canonFlag        map[string]imap.Flag
	canonMailboxAttr map[string]imap.MailboxAttr
)

func canonInit() {
	flags := []imap.Flag{
		imap.FlagSeen,
		imap.FlagAnswered,
		imap.FlagFlagged,
		imap.FlagDeleted,
		imap.FlagDraft,
		imap.FlagForwarded,
		imap.FlagMDNSent,
		imap.FlagJunk,
		imap.FlagNotJunk,
		imap.FlagPhishing,
		imap.FlagImportant,
	}
	mailboxAttrs := []imap.MailboxAttr{
		imap.MailboxAttrNonExistent,
		imap.MailboxAttrNoInferiors,
		imap.MailboxAttrNoSelect,
		imap.MailboxAttrHasChildren,
		imap.MailboxAttrHasNoChildren,
		imap.MailboxAttrMarked,
		imap.MailboxAttrUnmarked,
		imap.MailboxAttrSubscribed,
		imap.MailboxAttrRemote,
		imap.MailboxAttrAll,
		imap.MailboxAttrArchive,
		imap.MailboxAttrDrafts,
		imap.MailboxAttrFlagged,
		imap.MailboxAttrJunk,
		imap.MailboxAttrSent,
		imap.MailboxAttrTrash,
		imap.MailboxAttrImportant,
	}

	canonFlag = make(map[string]imap.Flag)
	for _, flag := range flags {
		canonFlag[strings.ToLower(string(flag))] = flag
	}

	canonMailboxAttr = make(map[string]imap.MailboxAttr)
	for _, attr := range mailboxAttrs {
		canonMailboxAttr[strings.ToLower(string(attr))] = attr
	}
}

func canonicalFlag(s string) imap.Flag {
	canonOnce.Do(canonInit)
	if flag, ok := canonFlag[strings.ToLower(s)]; ok {
		return flag
	}
	return imap.Flag(s)
}

func canonicalMailboxAttr(s string) imap.MailboxAttr {
	canonOnce.Do(canonInit)
	if attr, ok := canonMailboxAttr[strings.ToLower(s)]; ok {
		return attr
	}
	return imap.MailboxAttr(s)
}

func canonicalCap(s string) imap.Cap {
	// Only two caps are not fully uppercase
	for _, cap := range []imap.Cap{imap.CapIMAP4rev1, imap.CapIMAP4rev2} {
		if strings.EqualFold(s, string(cap)) {
			return cap
		}
	}
	return imap.Cap(strings.ToUpper(s))
}
