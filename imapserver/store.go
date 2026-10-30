package imapserver

import (
	"strings"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/internal"
	"github.com/emersion/go-imap/v2/internal/imapwire"
)

func (c *Conn) handleStore(dec *imapwire.Decoder, numKind NumKind) error {
	var (
		numSet imap.NumSet
		item   string
	)
	if !dec.ExpectSP() || !dec.ExpectNumSet(numKind.wire(), &numSet) || !dec.ExpectSP() || !dec.ExpectAtom(&item) || !dec.ExpectSP() {
		return dec.Err()
	}

	item = strings.ToUpper(item)
	silent := strings.HasSuffix(item, ".SILENT")
	item = strings.TrimSuffix(item, ".SILENT")

	var op imap.StoreFlagsOp
	switch {
	case strings.HasPrefix(item, "+"):
		op = imap.StoreFlagsAdd
		item = strings.TrimPrefix(item, "+")
	case strings.HasPrefix(item, "-"):
		op = imap.StoreFlagsDel
		item = strings.TrimPrefix(item, "-")
	default:
		op = imap.StoreFlagsSet
	}

	switch item {
	case "FLAGS":
		return c.handleStoreFlags(dec, numSet, op, silent)
	case "X-GM-LABELS":
		return c.handleStoreGmailLabels(dec, numSet, op, silent)
	default:
		return newClientBugError("STORE can only change FLAGS or X-GM-LABELS")
	}
}

func (c *Conn) handleStoreFlags(dec *imapwire.Decoder, numSet imap.NumSet, op imap.StoreFlagsOp, silent bool) error {
	flags, err := readStoreValues(dec, internal.ExpectFlag)
	if err != nil {
		return err
	}

	if err := c.checkState(imap.ConnStateSelected); err != nil {
		return err
	}

	w := &FetchWriter{conn: c}
	options := imap.StoreOptions{}
	return c.session.Store(w, numSet, &imap.StoreFlags{
		Op:     op,
		Silent: silent,
		Flags:  flags,
	}, &options)
}

func (c *Conn) handleStoreGmailLabels(dec *imapwire.Decoder, numSet imap.NumSet, op imap.StoreFlagsOp, silent bool) error {
	labels, err := readStoreValues(dec, internal.ExpectGmailLabel)
	if err != nil {
		return err
	}

	if err := c.checkState(imap.ConnStateSelected); err != nil {
		return err
	}

	session, ok := c.session.(SessionGmail)
	if !ok {
		return newClientBugError("STORE X-GM-LABELS is not supported")
	}
	w := &FetchWriter{conn: c}
	return session.StoreGmailLabels(w, numSet, &imap.StoreGmailLabels{
		Op:     op,
		Silent: silent,
		Labels: labels,
	})
}

// readStoreValues reads the values of a STORE data item up to the end of the
// line, either as a parenthesized list or as space-separated values.
func readStoreValues[T any](dec *imapwire.Decoder, expect func(*imapwire.Decoder) (T, error)) ([]T, error) {
	var values []T
	isList, err := dec.List(func() error {
		v, err := expect(dec)
		if err != nil {
			return err
		}
		values = append(values, v)
		return nil
	})
	if err != nil {
		return nil, err
	} else if !isList {
		for {
			v, err := expect(dec)
			if err != nil {
				return nil, err
			}
			values = append(values, v)

			if !dec.SP() {
				break
			}
		}
	}
	if !dec.ExpectCRLF() {
		return nil, dec.Err()
	}
	return values, nil
}
