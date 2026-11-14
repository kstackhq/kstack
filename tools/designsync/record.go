package main

import (
	"fmt"
	"io"
)

// recordCmd sets the named entries' fingerprints to what the working tree
// holds: the last step of a sync, once the design and the code match. With a
// stamps file it sets the design's too: each entry's node, name and hash.
func (t tool) recordCmd(stampsPath string, ids []string, out io.Writer) error {
	m, _, err := t.loadManifest()
	if err != nil {
		return err
	}
	stamps, err := loadStamps(stampsPath)
	if err != nil {
		return err
	}
	versions, err := t.lockVersions()
	if err != nil {
		return err
	}
	design := map[string]stamped{}
	for _, s := range stamps {
		if s.Stamp != nil {
			design[s.Stamp.ID] = s
		}
	}
	byID := map[string]*component{}
	for _, c := range m.entries() {
		byID[c.ID] = c
	}

	for _, id := range ids {
		c := byID[id]
		if c == nil {
			return fmt.Errorf("%s is not in %s", id, manifestPath)
		}
		if stampsPath != "" {
			s, ok := design[id]
			if !ok || s.Hash == "" {
				return fmt.Errorf("%s: the stamps file has no hashed component or screen carrying its stamp", id)
			}
			c.Figma.Node, c.Figma.Name, c.Figma.Hash = s.Node, s.Name, s.Hash
			// A component no code draws has only its design to record.
			if !c.built() {
				continue
			}
		}
		if err := t.recordCode(c, versions); err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
	}
	if err := t.saveManifest(m); err != nil { // coverage:ignore only an unwritable checkout fails here
		return err
	}
	for _, id := range ids {
		fmt.Fprintln(out, "recorded", id)
	}
	return nil
}

// recordCode sets the entry's code fingerprints: an icon's version to the
// lockfile's, a component's blobs to its files'.
func (t tool) recordCode(c *component, versions map[string]string) error {
	if !c.built() {
		return fmt.Errorf("no code draws it yet: add its code to %s first", manifestPath)
	}
	if p := c.Package; p != nil {
		v, ok := versions[p.Name]
		if !ok {
			return fmt.Errorf("%s is not a dependency in %s", p.Name, lockPath)
		}
		p.Version = v
	}
	for i := range c.Code {
		data, err := t.read(c.Code[i].Path)
		if err != nil {
			return err
		}
		c.Code[i].Blob = blobHash(data)
	}
	return nil
}
