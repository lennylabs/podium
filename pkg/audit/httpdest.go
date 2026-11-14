package audit

import (
	"bytes"
	"context"
	"net/http"
)

// ndjsonContentType is the media type of an HTTP destination's batch.
const ndjsonContentType = "application/x-ndjson"

// httpDest is the §8.3 HTTP destination: it POSTs each batch as NDJSON,
// one record per line, through the stream's transport.
//
// Spec: §8.3
type httpDest struct {
	url string
	tr  *transport
}

// send POSTs batch on ctx, which carries the forwarder's request deadline.
func (d *httpDest) send(ctx context.Context, batch []record) (sendResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.url, bytes.NewReader(ndjson(batch)))
	// d.url is a URL Open already parsed, so this branch does not run; the
	// error is kept rather than discarded.
	if err != nil {
		return sendResult{}, err
	}
	setHeaders(req, d.tr.headers, ndjsonContentType)
	return d.tr.do(req)
}

// ndjson joins the batch's lines, each followed by a newline.
func ndjson(batch []record) []byte {
	size := 0
	for _, r := range batch {
		size += len(r.line) + 1
	}
	body := make([]byte, 0, size)
	for _, r := range batch {
		body = append(append(body, r.line...), '\n')
	}
	return body
}

// setHeaders copies the headers-file headers onto req and sets the content
// type, which a headers file cannot override.
func setHeaders(req *http.Request, headers http.Header, contentType string) {
	for name, values := range headers {
		req.Header[name] = append([]string(nil), values...)
	}
	req.Header.Set("Content-Type", contentType)
}
