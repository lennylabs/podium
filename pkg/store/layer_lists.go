package store

import (
	"errors"
	"fmt"
	"strings"
)

// ErrLayerListEntry reports a LayerConfig whose groups or users list holds an
// entry with a line feed. The store writes nothing.
var ErrLayerListEntry = errors.New("store: layer visibility entry contains a line feed")

// layerListSeparator joins a layer's groups and users entries into one column
// in the SQL stores.
const layerListSeparator = "\n"

// checkLayerLists refuses a LayerConfig whose groups or users list holds an
// entry with a line feed. The SQL stores join the entries on a line feed and
// split them on read, so such an entry would read back as several entries and
// widen the layer to each of them. Every request and configuration path
// refuses the entry before it reaches the store (§4.6); this check keeps a
// write path that skips that refusal from storing a list that does not read
// back as written. The memory store applies it too, so every backend accepts
// the same configs.
//
// Spec: §4.6
func checkLayerLists(cfg LayerConfig) error {
	for _, list := range []struct {
		field  string
		values []string
	}{{"groups", cfg.Groups}, {"users", cfg.Users}} {
		for i, v := range list.values {
			if strings.Contains(v, layerListSeparator) {
				return fmt.Errorf("%w: layer %s %s[%d]", ErrLayerListEntry, cfg.ID, list.field, i)
			}
		}
	}
	return nil
}

// layerListColumns returns the groups and users columns the SQL stores write
// for cfg, or ErrLayerListEntry.
//
// Spec: §4.6
func layerListColumns(cfg LayerConfig) (groups, users string, err error) {
	if err := checkLayerLists(cfg); err != nil {
		return "", "", err
	}
	return strings.Join(cfg.Groups, layerListSeparator), strings.Join(cfg.Users, layerListSeparator), nil
}
