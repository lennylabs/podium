package layer

import (
	"fmt"
	"strings"
	"unicode"
)

// CheckEntry reports why v is not a §4.6 visibility entry, or nil when it is
// one. An entry is a non-empty string that holds no control character (Unicode
// general category Cc, which covers the line feed). name labels v in the
// error, such as "users[1]".
//
// The rule exists because no legitimate user or group name holds a control
// character, and the SQL metadata stores join a layer's entries on a line feed
// and split them on read: an entry "alice@acme.com\nbob@acme.com" would come
// back as two entries and widen the layer to bob. An empty entry names no
// principal, and it does not survive that encoding either, because a list
// holding one empty entry reads back as no list. Surrounding white space is
// kept: entries are matched byte for byte (§7.3.1 compares them the same way),
// and a group display name can carry a space.
//
// Spec: §4.6
func CheckEntry(name, v string) error {
	if v == "" {
		return fmt.Errorf("%s is empty; a visibility entry is a non-empty string with no control character", name)
	}
	if strings.ContainsFunc(v, unicode.IsControl) {
		return fmt.Errorf("%s contains a control character; a visibility entry is a non-empty string with no control character", name)
	}
	return nil
}

// CheckEntries applies CheckEntry to each value of the list named field and
// returns the first refusal, which names the entry by its index.
//
// Spec: §4.6
func CheckEntries(field string, values []string) error {
	for i, v := range values {
		if err := CheckEntry(fmt.Sprintf("%s[%d]", field, i), v); err != nil {
			return err
		}
	}
	return nil
}
