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
// Append (§8.3 Loss accounting). The full slice expression caps the
// capacity at the length, so append copies into a new array: r.line is
// shared with the other destinations and must never gain the newline in
// place.
func (d *stdoutDest) write(r record) error {
	line := append(r.line[:len(r.line):len(r.line)], '\n')
	_, err := d.w.Write(line)
	return err
}
