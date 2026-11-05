package imapclient

import (
	"errors"

	"github.com/emersion/go-imap/v2/internal/imapwire"
)

// ParseError is the error of a response the client could not parse. The
// connection is closed after it, as after any read error, but sending the
// same command again gets the same response and fails the same way. Commands
// pending when it happened fail with it.
type ParseError struct {
	Err error
}

func (err *ParseError) Error() string {
	return "imapclient: cannot parse the server's response: " + err.Err.Error()
}

func (err *ParseError) Unwrap() error {
	return err.Err
}

// asParseError wraps err, the error of reading a response, in a ParseError
// when the response's content was at fault: decErr, the decoder's own
// error, is nil because a reader rejected what it decoded, or is an
// expectation the content did not meet. Errors of the connection, such as
// an early EOF, a timeout or a closed socket, are returned unchanged.
func asParseError(err, decErr error) error {
	var expect *imapwire.DecoderExpectError
	if decErr == nil || errors.As(decErr, &expect) {
		return &ParseError{Err: err}
	}
	return err
}
