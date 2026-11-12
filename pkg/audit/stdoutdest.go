package audit

import "io"

// stdoutDest is the §8.3 stdout destination: it writes each chained record
// as one JSON line. It is synchronous, and the stream calls write while it
// holds s.mu, so lines never interleave and arrive in chaining order.
//
// Spec: §8.3
type stdoutDest struct {
	w io.Writer
}

// write writes r's line and its newline in a single Write call, so a
// reader of the pipe never sees a partial record from a split write. The
// caller counts a returned error with write_error and never returns it from
// Append (§8.3 Loss accounting).
func (d *stdoutDest) write(r record) error {
	line := make([]byte, 0, len(r.line)+1)
	line = append(append(line, r.line...), '\n')
	_, err := d.w.Write(line)
	return err
}
