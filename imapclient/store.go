package imapclient

import (
	"fmt"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/internal"
	"github.com/emersion/go-imap/v2/internal/imapwire"
)

// Store sends a STORE command.
//
// Unless StoreFlags.Silent is set, the server will return the updated values.
//
// A nil options pointer is equivalent to a zero options value.
func (c *Client) Store(numSet imap.NumSet, store *imap.StoreFlags, options *imap.StoreOptions) *FetchCommand {
	cmd, enc := c.beginStore(numSet, options, store.Op, "FLAGS", store.Silent)
	enc.SP().List(len(store.Flags), func(i int) {
		enc.Flag(store.Flags[i])
	})
	enc.end()
	return cmd
}

// StoreGmailLabels sends a STORE command which alters X-GM-LABELS.
//
// Unless StoreGmailLabels.Silent is set, the server will return the updated
// labels.
//
// A nil options pointer is equivalent to a zero options value.
//
// This requires the X-GM-EXT-1 extension.
func (c *Client) StoreGmailLabels(numSet imap.NumSet, store *imap.StoreGmailLabels, options *imap.StoreOptions) *FetchCommand {
	cmd, enc := c.beginStore(numSet, options, store.Op, "X-GM-LABELS", store.Silent)
	enc.SP().List(len(store.Labels), func(i int) {
		internal.EncodeGmailLabel(enc.Encoder, store.Labels[i])
	})
	enc.end()
	return cmd
}

// beginStore writes a STORE command up to and including the data item name.
func (c *Client) beginStore(numSet imap.NumSet, options *imap.StoreOptions, op imap.StoreFlagsOp, item string, silent bool) (*FetchCommand, *commandEncoder) {
	cmd := &FetchCommand{
		numSet: numSet,
		msgs:   make(chan *FetchMessageData, 128),
	}
	enc := c.beginCommand(uidCmdName("STORE", imapwire.NumSetKind(numSet)), cmd)
	enc.SP().NumSet(numSet).SP()
	if options != nil && options.UnchangedSince != 0 {
		enc.Special('(').Atom("UNCHANGEDSINCE").SP().ModSeq(options.UnchangedSince).Special(')').SP()
	}
	switch op {
	case imap.StoreFlagsSet:
		// nothing to do
	case imap.StoreFlagsAdd:
		enc.Special('+')
	case imap.StoreFlagsDel:
		enc.Special('-')
	default:
		panic(fmt.Errorf("imapclient: unknown store flags op: %v", op))
	}
	enc.Atom(item)
	if silent {
		enc.Atom(".SILENT")
	}
	return cmd, enc
}
