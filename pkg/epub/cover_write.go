package epub

import (
	"archive/zip"
	"bytes"
	"cmp"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type opfEdit struct {
	start       int
	end         int
	replacement string
}

type opfNode struct {
	tag                             xml.StartElement
	start, openEnd, closeStart, end int
}

func (n *opfNode) attr(name string) string {
	return opfAttribute(n.tag.Attr, name)
}

func (n *opfNode) withAttrs(source []byte, updates map[string]string) (string, error) {
	open := source[n.start:n.openEnd]
	token, err := xml.NewDecoder(bytes.NewReader(open)).RawToken()
	if err != nil {
		return "", fmt.Errorf("%w: parse element token: %v", ErrMalformedOPF, err)
	}
	start, ok := token.(xml.StartElement)
	if !ok {
		return "", fmt.Errorf("%w: unexpected element token", ErrMalformedOPF)
	}
	fullName := func(name xml.Name) string {
		if name.Space == "" {
			return name.Local
		}
		return name.Space + ":" + name.Local
	}

	var out strings.Builder
	out.WriteString("<" + fullName(start.Name))

	written := make(map[string]bool, len(updates))
	for i, attr := range start.Attr {
		key := fullName(attr.Name)
		resolved := n.tag.Attr[i].Name
		if val, hasUpdate := updates[resolved.Local]; hasUpdate && (resolved.Space == "" || resolved.Space == opfNamespace) {
			written[resolved.Local] = true
			if val != "" {
				fmt.Fprintf(&out, " %s=\"%s\"", key, escapeXMLAttr(val))
			}
			continue
		}
		fmt.Fprintf(&out, " %s=\"%s\"", key, escapeXMLAttr(attr.Value))
	}

	keys := make([]string, 0, len(updates))
	for k := range updates {
		if !written[k] && updates[k] != "" {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	for _, k := range keys {
		fmt.Fprintf(&out, " %s=\"%s\"", k, escapeXMLAttr(updates[k]))
	}

	if n.closeStart == n.end {
		out.WriteString("/>")
	} else {
		out.WriteString(">")
		out.Write(source[n.openEnd:n.end])
	}
	return out.String(), nil
}

type parsedCoverOPF struct {
	ids                 map[string]bool
	metadataEnd         int
	manifestStart       int
	manifestOpenEnd     int
	manifestEnd         int
	manifestSelfClosing bool
	coverMetas          []*opfNode
	manifestItems       []*opfNode
	hasModified         bool
}

func scanOPFForCover(source []byte) (*parsedCoverOPF, error) {
	d := xml.NewDecoder(bytes.NewReader(source))
	res := &parsedCoverOPF{
		ids: make(map[string]bool),
	}
	depth := 0
	inMetadata := false
	inManifest := false
	metadataOpenEnd := 0
	var current *opfNode

	for {
		offset := int(d.InputOffset())
		token, err := d.Token()
		if err == io.EOF {
			if res.metadataEnd == 0 || res.manifestStart == 0 {
				return nil, fmt.Errorf("%w: package metadata or manifest not found", ErrMalformedOPF)
			}
			return res, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrMalformedOPF, err)
		}

		switch tok := token.(type) {
		case xml.StartElement:
			depth++
			for _, attr := range tok.Attr {
				if attr.Name.Local == "id" && attr.Value != "" {
					res.ids[attr.Value] = true
				}
			}
			if depth == 2 && (tok.Name.Space == opfNamespace || tok.Name.Space == "") {
				switch tok.Name.Local {
				case "metadata":
					if res.metadataEnd == 0 {
						inMetadata = true
						metadataOpenEnd = int(d.InputOffset())
					}
				case "manifest":
					if res.manifestStart == 0 {
						inManifest = true
						res.manifestStart = offset
						res.manifestOpenEnd = int(d.InputOffset())
					}
				}
			} else if depth == 3 {
				if inMetadata && tok.Name.Local == "meta" && (tok.Name.Space == opfNamespace || tok.Name.Space == "") {
					if opfAttribute(tok.Attr, "property") == "dcterms:modified" && opfAttribute(tok.Attr, "refines") == "" {
						res.hasModified = true
					}
					if opfAttribute(tok.Attr, "name") == "cover" {
						current = &opfNode{tag: tok, start: offset, openEnd: int(d.InputOffset())}
					}
				} else if inManifest && tok.Name.Local == "item" && (tok.Name.Space == opfNamespace || tok.Name.Space == "") {
					current = &opfNode{tag: tok, start: offset, openEnd: int(d.InputOffset())}
				}
			}

		case xml.EndElement:
			if inMetadata && depth == 2 {
				if metadataOpenEnd == int(d.InputOffset()) {
					return nil, fmt.Errorf("%w: empty package metadata", ErrMalformedOPF)
				}
				inMetadata = false
				res.metadataEnd = offset
			} else if inManifest && depth == 2 {
				inManifest = false
				if res.manifestOpenEnd == int(d.InputOffset()) {
					res.manifestSelfClosing = true
					res.manifestEnd = int(d.InputOffset())
				} else {
					res.manifestEnd = offset
				}
			} else if current != nil && depth == 3 {
				current.closeStart = offset
				current.end = int(d.InputOffset())
				if inMetadata {
					res.coverMetas = append(res.coverMetas, current)
				} else if inManifest {
					res.manifestItems = append(res.manifestItems, current)
				}
				current = nil
			}
			depth--
		}
	}
}

func coverExtensionForMediaType(mediaType string) (string, error) {
	switch mediaType {
	case "image/jpeg":
		return ".jpg", nil
	case "image/png":
		return ".png", nil
	default:
		return "", fmt.Errorf("unsupported EPUB cover media type %q", mediaType)
	}
}

func safeCoverZipPath(opfDir, opfZipPath, href string) (string, bool) {
	if strings.TrimSpace(href) == "" {
		return "", false
	}
	unescaped, err := url.PathUnescape(href)
	if err != nil {
		unescaped = href
	}
	slashed := filepath.ToSlash(unescaped)
	if strings.HasPrefix(slashed, "/") {
		return "", false
	}
	cleanHref := path.Clean(slashed)
	if cleanHref == "" || cleanHref == "." || cleanHref == ".." || strings.HasPrefix(cleanHref, "../") {
		return "", false
	}
	zipPath := cleanHref
	if opfDir != "" && opfDir != "." {
		zipPath = path.Clean(path.Join(opfDir, cleanHref))
	}
	if zipPath == "" || zipPath == "." || zipPath == ".." || strings.HasPrefix(zipPath, "../") || strings.HasPrefix(zipPath, "/") {
		return "", false
	}
	normOPF := path.Clean(filepath.ToSlash(opfZipPath))
	if strings.EqualFold(zipPath, "mimetype") ||
		strings.EqualFold(zipPath, "META-INF/container.xml") ||
		strings.EqualFold(zipPath, normOPF) {
		return "", false
	}
	return zipPath, true
}

func hasToken(spaceSeparated, token string) bool {
	return slices.Contains(strings.Fields(spaceSeparated), token)
}

func addToken(spaceSeparated, token string) string {
	fields := strings.Fields(spaceSeparated)
	if slices.Contains(fields, token) {
		return strings.Join(fields, " ")
	}
	fields = append(fields, token)
	return strings.Join(fields, " ")
}

func removeToken(spaceSeparated, token string) string {
	fields := strings.Fields(spaceSeparated)
	filtered := make([]string, 0, len(fields))
	for _, f := range fields {
		if f != token {
			filtered = append(filtered, f)
		}
	}
	return strings.Join(filtered, " ")
}

func selectCoverManifestNode(items []*opfNode, coverMetaItemID string) *opfNode {
	// Strategy 1: EPUB 3 properties="cover-image"
	for _, item := range items {
		if hasToken(item.attr("properties"), "cover-image") {
			return item
		}
	}
	// Strategy 2: EPUB 2 / Calibre <meta name="cover" content="item_id"/>
	if coverMetaItemID != "" {
		for _, item := range items {
			if item.attr("id") == coverMetaItemID {
				return item
			}
		}
	}
	// Strategy 3: Fallback matching image media-type and "cover" in href or ID
	for _, item := range items {
		if strings.HasPrefix(item.attr("media-type"), "image/") {
			lowerHref := strings.ToLower(item.attr("href"))
			lowerID := strings.ToLower(item.attr("id"))
			if strings.Contains(lowerHref, "cover") || strings.Contains(lowerID, "cover") {
				return item
			}
		}
	}
	return nil
}

// updateOPFCover returns the updated OPF XML bytes and the ZIP entry path where the
// cover image bytes must be written (replacing an existing cover entry or adding a new one).
func updateOPFCover(source []byte, parsed *parsedOPF, zr *zip.Reader, opfZipPath, opfDir, mediaType string) ([]byte, string, error) {
	ext, err := coverExtensionForMediaType(mediaType)
	if err != nil {
		return nil, "", err
	}
	scanned, err := scanOPFForCover(source)
	if err != nil {
		return nil, "", err
	}

	newID := func(base string) string {
		id := base
		for i := 2; scanned.ids[id]; i++ {
			id = fmt.Sprintf("%s-%d", base, i)
		}
		scanned.ids[id] = true
		return id
	}

	var edits []opfEdit
	var metaAdditions []string
	var manifestAdditions []string

	chosen := selectCoverManifestNode(scanned.manifestItems, parsed.coverMetaItemID)
	var coverZipPath string
	var coverItemID string

	if chosen != nil {
		if safePath, ok := safeCoverZipPath(opfDir, opfZipPath, chosen.attr("href")); ok {
			if existing := findZipFile(zr.File, safePath); existing != nil {
				coverZipPath = path.Clean(filepath.ToSlash(existing.Name))
			} else {
				coverZipPath = safePath
			}
			coverItemID = chosen.attr("id")
			updates := map[string]string{
				"media-type": mediaType,
			}
			if coverItemID == "" {
				coverItemID = newID("cover-image")
				updates["id"] = coverItemID
			}
			if parsed.isEPUB3 {
				updates["properties"] = addToken(chosen.attr("properties"), "cover-image")
			}
			rewritten, err := chosen.withAttrs(source, updates)
			if err != nil {
				return nil, "", err
			}
			edits = append(edits, opfEdit{start: chosen.start, end: chosen.end, replacement: rewritten})
		} else {
			chosen = nil
		}
	}

	// Ensure no other manifest item retains a duplicate cover-image property in EPUB 3.
	if parsed.isEPUB3 {
		for _, item := range scanned.manifestItems {
			if item == chosen {
				continue
			}
			props := item.attr("properties")
			if hasToken(props, "cover-image") {
				rewritten, err := item.withAttrs(source, map[string]string{
					"properties": removeToken(props, "cover-image"),
				})
				if err != nil {
					return nil, "", err
				}
				edits = append(edits, opfEdit{start: item.start, end: item.end, replacement: rewritten})
			}
		}
	}

	if chosen == nil {
		coverItemID = newID("cover-image")
		existingHrefs := make(map[string]bool, len(scanned.manifestItems))
		for _, item := range scanned.manifestItems {
			existingHrefs[strings.ToLower(path.Clean(filepath.ToSlash(item.attr("href"))))] = true
		}

		href := "cover" + ext
		for i := 1; ; i++ {
			candidateZip, ok := safeCoverZipPath(opfDir, opfZipPath, href)
			if ok && !existingHrefs[strings.ToLower(href)] && findZipFile(zr.File, candidateZip) == nil {
				coverZipPath = candidateZip
				break
			}
			if i == 1 {
				href = "callimachus-cover" + ext
			} else {
				href = fmt.Sprintf("callimachus-cover-%d%s", i, ext)
			}
		}

		propsAttr := ""
		if parsed.isEPUB3 {
			propsAttr = ` properties="cover-image"`
		}
		manifestAdditions = append(manifestAdditions, fmt.Sprintf(
			`<item xmlns="%s" id="%s" href="%s" media-type="%s"%s/>`,
			opfNamespace, escapeXMLAttr(coverItemID), escapeXMLAttr(href), escapeXMLAttr(mediaType), propsAttr,
		))
	}

	// Update existing <meta name="cover" .../> tags, or add one for EPUB 2.
	if len(scanned.coverMetas) > 0 {
		for _, meta := range scanned.coverMetas {
			rewritten, err := meta.withAttrs(source, map[string]string{"content": coverItemID})
			if err != nil {
				return nil, "", err
			}
			edits = append(edits, opfEdit{start: meta.start, end: meta.end, replacement: rewritten})
		}
	} else if !parsed.isEPUB3 {
		metaAdditions = append(metaAdditions, fmt.Sprintf(
			`<meta xmlns="%s" name="cover" content="%s"/>`,
			opfNamespace, escapeXMLAttr(coverItemID),
		))
	}

	if parsed.isEPUB3 && !scanned.hasModified {
		metaAdditions = append(metaAdditions, fmt.Sprintf(
			`<meta xmlns="%s" property="dcterms:modified">%s</meta>`,
			opfNamespace, time.Now().UTC().Format(time.RFC3339),
		))
	}

	if len(metaAdditions) > 0 {
		var ins strings.Builder
		for _, item := range metaAdditions {
			ins.WriteString("\n    " + item)
		}
		ins.WriteString("\n  ")
		edits = append(edits, opfEdit{start: scanned.metadataEnd, end: scanned.metadataEnd, replacement: ins.String()})
	}

	if len(manifestAdditions) > 0 {
		if scanned.manifestSelfClosing {
			rawTag := strings.TrimSuffix(string(source[scanned.manifestStart:scanned.manifestOpenEnd]), "/>")
			tagName := rawTag[1 : strings.IndexAny(rawTag[1:], " \t\r\n/>")+1]
			var expanded strings.Builder
			expanded.WriteString(rawTag + ">")
			for _, item := range manifestAdditions {
				expanded.WriteString("\n    " + item)
			}
			expanded.WriteString("\n  </" + tagName + ">")
			edits = append(edits, opfEdit{start: scanned.manifestStart, end: scanned.manifestEnd, replacement: expanded.String()})
		} else {
			var ins strings.Builder
			for _, item := range manifestAdditions {
				ins.WriteString("\n    " + item)
			}
			ins.WriteString("\n  ")
			edits = append(edits, opfEdit{start: scanned.manifestEnd, end: scanned.manifestEnd, replacement: ins.String()})
		}
	}

	slices.SortStableFunc(edits, func(a, b opfEdit) int {
		if c := cmp.Compare(a.start, b.start); c != 0 {
			return c
		}
		return cmp.Compare(a.end, b.end)
	})

	var out bytes.Buffer
	last := 0
	for _, e := range edits {
		if e.start < last {
			return nil, "", fmt.Errorf("%w: overlapping OPF cover edits", ErrMalformedOPF)
		}
		out.Write(source[last:e.start])
		out.WriteString(e.replacement)
		last = e.end
	}
	out.Write(source[last:])
	return out.Bytes(), coverZipPath, nil
}
