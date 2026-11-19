package epub

import (
	"bytes"
	"cmp"
	"encoding/xml"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
)

const (
	opfNamespace = "http://www.idpf.org/2007/opf"
	dcNamespace  = "http://purl.org/dc/elements/1.1/"
)

// Keep offsets into the original XML so untouched elements, namespace declarations,
// comments, and extension markup can be copied verbatim.
type metadataElement struct {
	tag                             xml.StartElement
	start, openEnd, closeStart, end int
	value                           string
}

func (e *metadataElement) attr(name string) string {
	return opfAttribute(e.tag.Attr, name)
}

func (e *metadataElement) isMeta() bool {
	return e.tag.Name.Local == "meta" && (e.tag.Name.Space == "" || e.tag.Name.Space == opfNamespace)
}

func readMetadataElements(source []byte) ([]*metadataElement, int, map[string]bool, error) {
	d := xml.NewDecoder(bytes.NewReader(source))
	var elems []*metadataElement
	var element *metadataElement
	var value strings.Builder
	depth := 0
	inMetadata := false
	metadataOpenEnd := 0
	metadataEnd := 0
	ids := make(map[string]bool)
	for {
		offset := int(d.InputOffset())
		token, err := d.Token()
		if err == io.EOF {
			if metadataEnd == 0 {
				return nil, 0, nil, fmt.Errorf("%w: package metadata not found", ErrMalformedOPF)
			}
			return elems, metadataEnd, ids, nil
		}
		if err != nil {
			return nil, 0, nil, fmt.Errorf("%w: %v", ErrMalformedOPF, err)
		}
		switch token := token.(type) {
		case xml.StartElement:
			depth++
			for _, attr := range token.Attr {
				if attr.Name.Local == "id" {
					ids[attr.Value] = true
				}
			}
			if metadataEnd == 0 && depth == 2 && token.Name.Local == "metadata" && (token.Name.Space == opfNamespace || token.Name.Space == "") {
				inMetadata = true
				metadataOpenEnd = int(d.InputOffset())
			}
			if inMetadata && depth == 3 {
				element = &metadataElement{tag: token, start: offset, openEnd: int(d.InputOffset())}
				value.Reset()
			}
		case xml.CharData:
			if element != nil {
				value.Write(token)
			}
		case xml.EndElement:
			if inMetadata && depth == 2 {
				if metadataOpenEnd == int(d.InputOffset()) {
					return nil, 0, nil, fmt.Errorf("%w: empty package metadata", ErrMalformedOPF)
				}
				inMetadata = false
				metadataEnd = offset
			}
			if element != nil && depth == 3 {
				element.closeStart, element.end = offset, int(d.InputOffset())
				element.value = strings.TrimSpace(value.String())
				elems = append(elems, element)
				element = nil
			}
			depth--
		}
	}
}

func (e *metadataElement) withValue(source []byte, value string) string {
	open := string(source[e.start:e.openEnd])
	if e.closeStart != e.end {
		return open + escapeXML(value) + string(source[e.closeStart:e.end])
	}
	name := open[1 : strings.IndexAny(open[1:], " \t\r\n/>")+1]
	return strings.TrimSuffix(open, "/>") + ">" + escapeXML(value) + "</" + name + ">"
}

func (e *metadataElement) withAttr(source []byte, name, value string) string {
	open := source[e.start:e.openEnd]
	// RawToken retains lexical prefixes so we can write the edited attribute
	// without changing the meaning of names declared elsewhere in the package.
	token, _ := xml.NewDecoder(bytes.NewReader(open)).RawToken()
	start := token.(xml.StartElement)
	fullName := func(name xml.Name) string {
		if name.Space == "" {
			return name.Local
		}
		return name.Space + ":" + name.Local
	}
	var out strings.Builder
	attributeName := name
	out.WriteString("<" + fullName(start.Name))
	for i, attr := range start.Attr {
		key := fullName(attr.Name)
		resolved := e.tag.Attr[i].Name
		if resolved.Local == name && (resolved.Space == "" || resolved.Space == opfNamespace) {
			attributeName = key
		} else {
			fmt.Fprintf(&out, " %s=\"%s\"", key, escapeXMLAttr(attr.Value))
		}
	}
	if value != "" {
		fmt.Fprintf(&out, " %s=\"%s\"", attributeName, escapeXMLAttr(value))
	}
	if e.closeStart == e.end {
		out.WriteString("/>")
	} else {
		out.WriteString(">")
		out.Write(source[e.openEnd:e.end])
	}
	return out.String()
}

func replacementMetadataXML(source []byte, m Metadata, parsed *parsedOPF) ([]byte, error) {
	elements, metadataEnd, ids, err := readMetadataElements(source)
	if err != nil {
		return nil, err
	}
	edits := make(map[*metadataElement]string)
	var added []string
	newID := func(base string) string {
		id := base
		for i := 2; ids[id]; i++ {
			id = fmt.Sprintf("%s-%d", base, i)
		}
		ids[id] = true
		return id
	}
	dcXML := func(name, value, attrs string) string {
		return fmt.Sprintf("<dc:%s xmlns:dc=\"%s\"%s>%s</dc:%s>", name, dcNamespace, attrs, escapeXML(value), name)
	}
	appendDC := func(name, value, attrs string) {
		added = append(added, dcXML(name, value, attrs))
	}
	appendMeta := func(name, value string) {
		added = append(added, fmt.Sprintf("<meta xmlns=\"%s\" name=\"%s\" content=\"%s\"/>", opfNamespace, name, escapeXMLAttr(value)))
	}
	appendProperty := func(property, value, attrs string) {
		added = append(added, fmt.Sprintf("<meta xmlns=\"%s\" property=\"%s\"%s>%s</meta>", opfNamespace, property, attrs, escapeXML(value)))
	}
	refinements := make(map[string]map[string]*metadataElement)
	dependents := make(map[string][]*metadataElement)
	for _, e := range elements {
		if target := e.attr("refines"); strings.HasPrefix(target, "#") {
			dependents[target] = append(dependents[target], e)
			if prop := e.attr("property"); prop != "" && e.isMeta() {
				if refinements[target] == nil {
					refinements[target] = make(map[string]*metadataElement)
				}
				refinements[target][prop] = e
			}
		}
	}
	refinement := func(e *metadataElement, property string) *metadataElement {
		return refinements["#"+e.attr("id")][property]
	}
	metaValue := func(e *metadataElement) string {
		if e == nil {
			return ""
		}
		return cmp.Or(e.value, e.attr("content"))
	}
	var remove func(*metadataElement)
	remove = func(e *metadataElement) {
		if edit, exists := edits[e]; exists && edit == "" {
			return
		}
		edits[e] = ""
		if id := e.attr("id"); id != "" {
			for _, child := range dependents["#"+id] {
				remove(child)
			}
		}
	}
	update := func(e *metadataElement, value string) {
		if value == "" {
			remove(e)
		} else if e.isMeta() && e.attr("content") != "" && e.value == "" {
			if e.attr("content") != value {
				edits[e] = e.withAttr(source, "content", value)
			}
		} else if e.value != value {
			edits[e] = e.withValue(source, value)
		}
	}
	dc := make(map[string][]*metadataElement)
	named := make(map[string][]*metadataElement)
	properties := make(map[string][]*metadataElement)
	var authors, mainTitles, titles, subtitles, collections, positions, editionISBNs []*metadataElement
	var packageISBN *metadataElement
	for _, e := range elements {
		switch {
		case e.tag.Name.Space == dcNamespace || e.tag.Name.Space == "":
			switch e.tag.Name.Local {
			case "title":
				switch metaValue(refinement(e, "title-type")) {
				case "subtitle":
					subtitles = append(subtitles, e)
				case "main":
					mainTitles = append(mainTitles, e)
				default:
					titles = append(titles, e)
				}
				continue
			case "creator":
				role := cmp.Or(e.attr("role"), metaValue(refinement(e, "role")))
				if role == "" || role == "aut" {
					authors = append(authors, e)
				}
				continue
			case "identifier":
				id := rawIdentifier{ID: e.attr("id"), Scheme: e.attr("scheme"), Value: e.value}
				if isbn13(id) != "" {
					if id.ID != "" && id.ID == parsed.uniqueIdentifier {
						packageISBN = e
					} else {
						editionISBNs = append(editionISBNs, e)
					}
				}
				continue
			case "date":
				if event := e.attr("event"); event == "" || event == "publication" {
					dc["date"] = append(dc["date"], e)
				}
				continue
			case "meta":
				// Some legacy OPFs leave the OPF namespace undeclared.
				if e.tag.Name.Space != "" {
					continue
				}
			default:
				dc[e.tag.Name.Local] = append(dc[e.tag.Name.Local], e)
				continue
			}
		case e.tag.Name.Space != opfNamespace || e.tag.Name.Local != "meta":
			continue
		}
		if e.attr("refines") == "" {
			if name := e.attr("name"); name != "" {
				named[name] = append(named[name], e)
			}
			property := e.attr("property")
			properties[property] = append(properties[property], e)
			switch property {
			case "belongs-to-collection":
				if kind := metaValue(refinement(e, "collection-type")); metaValue(e) != "" && (kind == "" || kind == "series") {
					collections = append(collections, e)
				}
			case "group-position":
				positions = append(positions, e)
			}
		}
	}

	original := parsed.metadata
	replaceDC := func(name, value, old string, candidates []*metadataElement) {
		if value == old {
			return
		}
		if value == "" {
			for _, e := range candidates {
				remove(e)
			}
		} else if i := slices.IndexFunc(candidates, func(e *metadataElement) bool { return e.value == old }); i >= 0 {
			update(candidates[i], value)
		} else {
			appendDC(name, value, "")
		}
	}
	replaceDC("title", m.Title, original.Title, append(mainTitles, titles...))
	replaceDC("language", cmp.Or(m.Language, "en"), original.Language, dc["language"])
	replaceDC("publisher", m.Publisher, original.Publisher, dc["publisher"])
	replaceDC("date", m.PublishDate, original.PublishDate, dc["date"])
	replaceDC("description", m.Description, original.Description, dc["description"])

	if m.Subtitle != original.Subtitle {
		for _, e := range subtitles {
			update(e, m.Subtitle)
		}
		for _, e := range properties["subtitle"] {
			update(e, m.Subtitle)
		}
		for _, e := range named["calibre:subtitle"] {
			update(e, m.Subtitle)
		}
		if m.Subtitle != "" && len(subtitles)+len(properties["subtitle"])+len(named["calibre:subtitle"]) == 0 {
			if parsed.isEPUB3 {
				id := newID("title-sub")
				appendDC("title", m.Subtitle, ` id="`+id+`"`)
				appendProperty("title-type", "subtitle", ` refines="#`+id+`"`)
			} else {
				appendMeta("calibre:subtitle", m.Subtitle)
			}
		}
	}

	if !slices.Equal(m.Authors, original.Authors) {
		// Match unchanged people first so reordering or removing authors keeps
		// their IDs and the refinements that describe them.
		slots := make([]*metadataElement, len(m.Authors))
		byName := make(map[string][]*metadataElement)
		used := make(map[*metadataElement]bool)
		for _, e := range authors {
			byName[e.value] = append(byName[e.value], e)
		}
		for i, name := range m.Authors {
			if matches := byName[name]; len(matches) > 0 {
				slots[i], byName[name] = matches[0], matches[1:]
				used[slots[i]] = true
			}
		}
		var spare []*metadataElement
		for _, e := range authors {
			if !used[e] {
				spare = append(spare, e)
			}
		}
		for i := range m.Authors {
			if slots[i] == nil && len(spare) > 0 {
				slots[i], spare = spare[0], spare[1:]
			}
		}
		for _, e := range spare {
			remove(e)
		}
		for i, name := range m.Authors {
			var creator string
			switch e := slots[i]; {
			case e == nil:
				creator = dcXML("creator", name, "")
			case e.value != name:
				creator = e.withValue(source, name)
			default:
				creator = string(source[e.start:e.end])
			}
			if i < len(authors) {
				edits[authors[i]] = creator
			} else {
				added = append(added, creator)
			}
		}
		for _, e := range authors[min(len(authors), len(slots)):] {
			edits[e] = ""
		}
	}

	if m.ISBN13 != original.ISBN13 {
		switch {
		case m.ISBN13 == "":
			for _, e := range editionISBNs {
				remove(e)
			}
			if packageISBN != nil {
				// The package identifier can also be the font obfuscation key.
				// Keep its value and ID while suppressing edition ISBN inference.
				edits[packageISBN] = packageISBN.withAttr(source, "scheme", "URI")
			}
		case len(editionISBNs) > 0:
			value := m.ISBN13
			if strings.HasPrefix(strings.ToLower(editionISBNs[0].value), "urn:isbn:") {
				value = "urn:isbn:" + value
			}
			update(editionISBNs[0], value)
		case parsed.isEPUB3:
			appendDC("identifier", "urn:isbn:"+m.ISBN13, "")
		default:
			added = append(added, fmt.Sprintf("<dc:identifier xmlns:dc=\"%s\" xmlns:opf=\"%s\" opf:scheme=\"ISBN\">%s</dc:identifier>", dcNamespace, opfNamespace, escapeXML(m.ISBN13)))
		}
	}

	setNamed := func(name, value string) {
		for _, e := range named[name] {
			update(e, value)
		}
		if value != "" && len(named[name]) == 0 {
			appendMeta(name, value)
		}
	}
	newSeriesID := ""
	if m.Series != original.Series {
		if m.Series == "" {
			for _, e := range collections {
				remove(e)
			}
		} else if len(collections) > 0 {
			update(collections[0], m.Series)
		} else if parsed.isEPUB3 && len(named["calibre:series"]) == 0 {
			newSeriesID = newID("series-c01")
			appendProperty("belongs-to-collection", m.Series, ` id="`+newSeriesID+`"`)
			appendProperty("collection-type", "series", ` refines="#`+newSeriesID+`"`)
		}
		if len(named["calibre:series"]) > 0 || (len(collections) == 0 && newSeriesID == "") {
			setNamed("calibre:series", m.Series)
		}
	}
	index := func(series Metadata) string {
		if series.SeriesIndex == nil || series.Series == "" {
			return ""
		}
		return formatFloat(*series.SeriesIndex)
	}
	if pos := index(m); pos != index(original) || (m.Series == "" && len(named["calibre:series_index"]) > 0) {
		if len(collections) > 0 && m.Series != "" {
			if e := refinement(collections[0], "group-position"); e != nil {
				positions = append(positions, e)
			}
		}
		for _, e := range positions {
			update(e, pos)
		}
		if newSeriesID != "" && pos != "" {
			appendProperty("group-position", pos, ` refines="#`+newSeriesID+`"`)
		}
		if len(named["calibre:series_index"]) > 0 || (len(positions) == 0 && newSeriesID == "") {
			setNamed("calibre:series_index", pos)
		}
	}
	if parsed.isEPUB3 && len(properties["dcterms:modified"]) == 0 {
		appendProperty("dcterms:modified", time.Now().UTC().Format(time.RFC3339), "")
	}

	var output bytes.Buffer
	last := 0
	for _, e := range elements {
		if replacement, ok := edits[e]; ok {
			output.Write(source[last:e.start])
			output.WriteString(replacement)
			last = e.end
		}
	}
	output.Write(source[last:metadataEnd])
	for _, element := range added {
		output.WriteString("\n    " + element)
	}
	if len(added) != 0 {
		output.WriteString("\n  ")
	}
	output.Write(source[metadataEnd:])
	return output.Bytes(), nil
}
