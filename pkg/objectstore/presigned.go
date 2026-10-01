package objectstore

import "net/url"

// PresignedSigV4 reports whether rawURL is an AWS Signature V4 presigned URL,
// identified by a non-empty X-Amz-Signature query parameter, which the S3
// backend appends. The filesystem backend's /objects/{key} route carries none.
// A consumer following a presigned URL sends no credential to a SigV4 URL,
// because S3 rejects a request that carries both query authentication and an
// Authorization header, and sends its registry token to every other URL,
// because the /objects route authorizes the read against the caller (§13.12).
// A URL that fails to parse is treated as not presigned, which attaches the
// token, the answer the registry route needs.
//
// Spec: §13.12.
func PresignedSigV4(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return u.Query().Get("X-Amz-Signature") != ""
}
