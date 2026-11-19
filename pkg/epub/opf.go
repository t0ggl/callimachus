package epub

import (
	"bytes"
	"cmp"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	reMetadata   = regexp.MustCompile(`(?s)<([a-zA-Z0-9_-]+:)?metadata\b[^>]*>.*?</([a-zA-Z0-9_-]+:)?metadata>`)
	reOnlyDigits = regexp.MustCompile(`[^0-9]`)
)

type ManifestItem struct {
	ID         string
	Href       string
	MediaType  string
	Properties string
}

type rawMetaTag struct {
	ID        string
	Name      string
	Content   string
	Property  string
	Refines   string
	Scheme    string
	TextValue string
}

type rawCreator struct {
	ID     string
	Role   string
	FileAs string
	Value  string
}

type rawTitle struct {
	ID    string
	Lang  string
	Value string
}

type rawIdentifier struct {
	ID     string
	Scheme string
	Value  string
}

// parsedOPF contains extracted OPF data needed for Metadata inspection,
// cover extraction, and metadata updating.
type parsedOPF struct {
	version          string
	uniqueIdentifier string
	metadata         Metadata
	manifestItems    []ManifestItem
	coverMetaItemID  string
	isEPUB3          bool
	identifiers      []rawIdentifier
}

// ParseOPF parses standalone OPF XML data and returns the extracted Metadata.
func ParseOPF(data []byte) (Metadata, error) {
	parsed, err := parseOPF(data)
	if err != nil {
		return Metadata{}, err
	}
	return parsed.metadata, nil
}

// parseOPF parses the OPF XML bytes and extracts metadata and manifest items.
func parseOPF(data []byte) (*parsedOPF, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))

	res := &parsedOPF{
		metadata: Metadata{
			Identifiers: make(map[string]string),
			ExtraMeta:   make(map[string]string),
		},
		manifestItems: make([]ManifestItem, 0),
	}

	var (
		titles       []rawTitle
		creators     []rawCreator
		identifiers  []rawIdentifier
		metaTags     []rawMetaTag
		inMetadata   bool
		inManifest   bool
		currentElem  string
		currentAttrs []xml.Attr
		textBuffer   bytes.Buffer
	)

	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: xml decode failed: %v", ErrMalformedOPF, err)
		}

		switch el := tok.(type) {
		case xml.StartElement:
			local := el.Name.Local
			switch local {
			case "package":
				for _, attr := range el.Attr {
					switch attr.Name.Local {
					case "version":
						res.version = attr.Value
						if strings.HasPrefix(attr.Value, "3.") {
							res.isEPUB3 = true
						}
					case "unique-identifier":
						res.uniqueIdentifier = attr.Value
					}
				}
			case "metadata":
				inMetadata = true
			case "manifest":
				inManifest = true
			case "item":
				if inManifest {
					item := ManifestItem{}
					for _, attr := range el.Attr {
						switch attr.Name.Local {
						case "id":
							item.ID = attr.Value
						case "href":
							item.Href = attr.Value
						case "media-type":
							item.MediaType = attr.Value
						case "properties":
							item.Properties = attr.Value
						}
					}
					res.manifestItems = append(res.manifestItems, item)
				}
			}

			if inMetadata {
				currentElem = local
				currentAttrs = el.Attr
				textBuffer.Reset()
			}

		case xml.EndElement:
			local := el.Name.Local
			if inMetadata {
				elemText := strings.TrimSpace(textBuffer.String())
				switch local {
				case "metadata":
					inMetadata = false
				case "title":
					t := rawTitle{Value: elemText}
					for _, attr := range currentAttrs {
						switch attr.Name.Local {
						case "id":
							t.ID = attr.Value
						case "lang":
							t.Lang = attr.Value
						}
					}
					titles = append(titles, t)
				case "creator":
					c := rawCreator{Value: elemText}
					for _, attr := range currentAttrs {
						switch attr.Name.Local {
						case "id":
							c.ID = attr.Value
						case "role":
							c.Role = attr.Value
						case "file-as":
							c.FileAs = attr.Value
						}
					}
					creators = append(creators, c)
				case "identifier":
					id := rawIdentifier{Value: elemText}
					for _, attr := range currentAttrs {
						switch attr.Name.Local {
						case "id":
							id.ID = attr.Value
						case "scheme":
							id.Scheme = attr.Value
						}
					}
					identifiers = append(identifiers, id)
				case "language":
					if res.metadata.Language == "" && elemText != "" {
						res.metadata.Language = elemText
					}
				case "publisher":
					if res.metadata.Publisher == "" && elemText != "" {
						res.metadata.Publisher = elemText
					}
				case "date":
					if res.metadata.PublishDate == "" && elemText != "" {
						res.metadata.PublishDate = elemText
					}
				case "description":
					if res.metadata.Description == "" && elemText != "" {
						res.metadata.Description = elemText
					}
				case "meta":
					m := rawMetaTag{TextValue: elemText}
					for _, attr := range currentAttrs {
						switch attr.Name.Local {
						case "id":
							m.ID = attr.Value
						case "name":
							m.Name = attr.Value
						case "content":
							m.Content = attr.Value
						case "property":
							m.Property = attr.Value
						case "refines":
							m.Refines = strings.TrimPrefix(attr.Value, "#")
						case "scheme":
							m.Scheme = attr.Value
						}
					}
					metaTags = append(metaTags, m)
				}
				currentElem = ""
			} else if inManifest && local == "manifest" {
				inManifest = false
			}

		case xml.CharData:
			if inMetadata && currentElem != "" {
				textBuffer.Write(el)
			}
		}
	}

	// Refines lookup maps for EPUB 3
	refinesProperty := make(map[string]map[string]string) // [refinesID][property] = value
	for _, m := range metaTags {
		if m.Refines != "" && m.Property != "" {
			val := m.TextValue
			if val == "" {
				val = m.Content
			}
			if refinesProperty[m.Refines] == nil {
				refinesProperty[m.Refines] = make(map[string]string)
			}
			refinesProperty[m.Refines][m.Property] = val
		}
	}

	// 1. Title and Subtitle resolution
	for _, t := range titles {
		if t.ID != "" {
			if props, ok := refinesProperty[t.ID]; ok {
				if props["title-type"] == "subtitle" {
					res.metadata.Subtitle = t.Value
					continue
				}
				if props["title-type"] == "main" && res.metadata.Title == "" {
					res.metadata.Title = t.Value
					continue
				}
			}
		}
		if res.metadata.Title == "" && t.Value != "" {
			res.metadata.Title = t.Value
		}
	}

	// Check Calibre / extra subtitle meta
	for _, m := range metaTags {
		if m.Name == "calibre:subtitle" && m.Content != "" {
			res.metadata.Subtitle = m.Content
		} else if m.Property == "subtitle" && m.TextValue != "" {
			res.metadata.Subtitle = m.TextValue
		}
	}

	// 2. Authors and SortAuthor resolution
	var authors []string
	for _, c := range creators {
		role := c.Role
		fileAs := c.FileAs
		if c.ID != "" {
			if props, ok := refinesProperty[c.ID]; ok {
				if r, ok := props["role"]; ok && role == "" {
					role = r
				}
				if fa, ok := props["file-as"]; ok && fileAs == "" {
					fileAs = fa
				}
			}
		}

		if role == "" || role == "aut" {
			if c.Value != "" {
				authors = append(authors, c.Value)
			}
			if fileAs != "" && res.metadata.SortAuthor == "" {
				res.metadata.SortAuthor = fileAs
			}
		}
	}
	if len(authors) == 0 {
		// Fallback: if no creator had role="aut", accept any creators
		for _, c := range creators {
			if c.Value != "" {
				authors = append(authors, c.Value)
			}
		}
	}
	res.metadata.Authors = authors

	// Check calibre:author_sort meta
	for _, m := range metaTags {
		if m.Name == "calibre:author_sort" && m.Content != "" && res.metadata.SortAuthor == "" {
			res.metadata.SortAuthor = m.Content
		}
	}

	// 3. Series and SeriesIndex resolution
	// EPUB 2 / Calibre check:
	for _, m := range metaTags {
		if m.Name == "calibre:series" && m.Content != "" {
			res.metadata.Series = m.Content
		}
		if m.Name == "calibre:series_index" && m.Content != "" {
			if f, err := strconv.ParseFloat(m.Content, 64); err == nil {
				res.metadata.SeriesIndex = &f
			}
		}
		if m.Name == "cover" && m.Content != "" {
			res.coverMetaItemID = m.Content
		}
	}

	// EPUB 3 belongs-to-collection check:
	for _, m := range metaTags {
		if m.Property == "belongs-to-collection" {
			seriesVal := m.TextValue
			if seriesVal == "" {
				seriesVal = m.Content
			}
			if seriesVal != "" {
				isSeries := true
				if m.ID != "" {
					if props, ok := refinesProperty[m.ID]; ok {
						if ct, exists := props["collection-type"]; exists && ct != "series" {
							isSeries = false
						}
						if gp, exists := props["group-position"]; exists && res.metadata.SeriesIndex == nil {
							if f, err := strconv.ParseFloat(gp, 64); err == nil {
								res.metadata.SeriesIndex = &f
							}
						}
					}
				}
				if isSeries && res.metadata.Series == "" {
					res.metadata.Series = seriesVal
				}
			}
		}
		// Also support simple EPUB 3 group-position without refines
		if m.Property == "group-position" && res.metadata.SeriesIndex == nil {
			val := m.TextValue
			if val == "" {
				val = m.Content
			}
			if f, err := strconv.ParseFloat(val, 64); err == nil {
				res.metadata.SeriesIndex = &f
			}
		}
	}

	// 4. Identifiers and ISBN13
	res.identifiers = identifiers
	for _, id := range identifiers {
		val := id.Value
		if val == "" {
			continue
		}
		scheme := strings.ToLower(id.Scheme)
		if scheme != "" {
			res.metadata.Identifiers[scheme] = val
		}
		if id.ID != "" {
			res.metadata.Identifiers[id.ID] = val
		}
		// Check for uuid format
		if strings.HasPrefix(strings.ToLower(val), "urn:uuid:") || strings.HasPrefix(strings.ToLower(val), "uuid:") {
			if _, exists := res.metadata.Identifiers["uuid"]; !exists {
				res.metadata.Identifiers["uuid"] = val
			}
		}
		// Check for ISBN
		cleanVal := reOnlyDigits.ReplaceAllString(val, "")
		if scheme == "uri" {
			continue
		}
		if scheme == "isbn" || scheme == "isbn13" || strings.Contains(strings.ToLower(id.ID), "isbn") {
			if res.metadata.ISBN13 == "" && len(cleanVal) == 13 {
				res.metadata.ISBN13 = cleanVal
			}
			if _, exists := res.metadata.Identifiers["isbn"]; !exists {
				res.metadata.Identifiers["isbn"] = val
			}
		} else if len(cleanVal) == 13 && (strings.HasPrefix(cleanVal, "978") || strings.HasPrefix(cleanVal, "979")) {
			if res.metadata.ISBN13 == "" {
				res.metadata.ISBN13 = cleanVal
			}
		}
	}

	// 5. ExtraMeta collection (preserve unknown tags)
	for _, m := range metaTags {
		if m.Name != "" && m.Content != "" {
			switch m.Name {
			case "calibre:series", "calibre:series_index", "calibre:subtitle", "cover":
				// Handled explicitly
			default:
				res.metadata.ExtraMeta[m.Name] = m.Content
			}
		} else if m.Property != "" {
			val := m.TextValue
			if val == "" {
				val = m.Content
			}
			switch m.Property {
			case "belongs-to-collection", "collection-type", "group-position", "title-type", "role", "file-as":
				// Handled explicitly
			default:
				if m.Refines == "" {
					res.metadata.ExtraMeta[m.Property] = val
				}
			}
		}
	}

	return res, nil
}

func escapeXML(s string) string {
	var buf bytes.Buffer
	if err := xml.EscapeText(&buf, []byte(s)); err != nil {
		return s
	}
	return buf.String()
}

func escapeXMLAttr(s string) string {
	s = escapeXML(s)
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}

func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func replacementMetadataXML(m, original Metadata, identifiers []rawIdentifier, uniqueID, coverID string, isEPUB3 bool) []byte {
	m.ExtraMeta = original.Clone().ExtraMeta
	delete(m.ExtraMeta, "calibre:author_sort")
	preserved := make([]rawIdentifier, 0, len(identifiers))
	for _, id := range identifiers {
		scheme := strings.ToLower(id.Scheme)
		digits := reOnlyDigits.ReplaceAllString(id.Value, "")
		isISBN := scheme != "uri" && (scheme == "isbn" || scheme == "isbn13" ||
			strings.Contains(strings.ToLower(id.ID), "isbn") ||
			(len(digits) == 13 && (strings.HasPrefix(digits, "978") || strings.HasPrefix(digits, "979"))))
		if isISBN {
			if id.ID == "" || id.ID != uniqueID {
				continue
			}
			// Preserve the package identifier, which can also be used for font obfuscation.
			id.Scheme = "URI"
		}
		preserved = append(preserved, id)
	}
	return serializeMetadataXML(m, uniqueID, coverID, isEPUB3, preserved)
}

// serializeMetadataXML constructs a complete <metadata> XML block.
func serializeMetadataXML(m Metadata, uniqueIDAttr string, coverMetaID string, isEPUB3 bool, identifiers []rawIdentifier) []byte {
	var buf bytes.Buffer
	buf.WriteString("  <metadata xmlns=\"http://www.idpf.org/2007/opf\" xmlns:dc=\"http://purl.org/dc/elements/1.1/\" xmlns:opf=\"http://www.idpf.org/2007/opf\">\n")

	// Title
	if m.Title != "" {
		fmt.Fprintf(&buf, "    <dc:title id=\"title-main\">%s</dc:title>\n", escapeXML(m.Title))
		if isEPUB3 {
			buf.WriteString("    <meta refines=\"#title-main\" property=\"title-type\">main</meta>\n")
		}
	}

	// Subtitle
	if m.Subtitle != "" {
		if isEPUB3 {
			fmt.Fprintf(&buf, "    <dc:title id=\"title-sub\">%s</dc:title>\n", escapeXML(m.Subtitle))
			buf.WriteString("    <meta refines=\"#title-sub\" property=\"title-type\">subtitle</meta>\n")
		}
		buf.WriteString(fmt.Sprintf("    <meta name=\"calibre:subtitle\" content=\"%s\"/>\n", escapeXMLAttr(m.Subtitle)))
	}

	// Authors
	for i, author := range m.Authors {
		creatorID := fmt.Sprintf("creator-%d", i+1)
		sortAuthor := i == 0 && m.SortAuthor != ""
		if isEPUB3 {
			fmt.Fprintf(&buf, "    <dc:creator id=\"%s\">%s</dc:creator>\n", creatorID, escapeXML(author))
			fmt.Fprintf(&buf, "    <meta refines=\"#%s\" property=\"role\" scheme=\"marc:relators\">aut</meta>\n", creatorID)
			if sortAuthor {
				fmt.Fprintf(&buf, "    <meta refines=\"#%s\" property=\"file-as\">%s</meta>\n", creatorID, escapeXML(m.SortAuthor))
			}
		} else if sortAuthor {
			fmt.Fprintf(&buf, "    <dc:creator id=\"%s\" opf:role=\"aut\" opf:file-as=\"%s\">%s</dc:creator>\n", creatorID, escapeXMLAttr(m.SortAuthor), escapeXML(author))
		} else {
			fmt.Fprintf(&buf, "    <dc:creator id=\"%s\" opf:role=\"aut\">%s</dc:creator>\n", creatorID, escapeXML(author))
		}
	}

	// Language
	lang := m.Language
	if lang == "" {
		lang = "en"
	}
	buf.WriteString(fmt.Sprintf("    <dc:language>%s</dc:language>\n", escapeXML(lang)))

	// Publisher
	if m.Publisher != "" {
		buf.WriteString(fmt.Sprintf("    <dc:publisher>%s</dc:publisher>\n", escapeXML(m.Publisher)))
	}

	// Date
	if m.PublishDate != "" {
		buf.WriteString(fmt.Sprintf("    <dc:date>%s</dc:date>\n", escapeXML(m.PublishDate)))
	}

	// Description
	if m.Description != "" {
		buf.WriteString(fmt.Sprintf("    <dc:description>%s</dc:description>\n", escapeXML(m.Description)))
	}

	// Explicit edition ISBN comes before the package identifiers.
	if m.ISBN13 != "" {
		if isEPUB3 {
			fmt.Fprintf(&buf, "    <dc:identifier>urn:isbn:%s</dc:identifier>\n", escapeXML(m.ISBN13))
		} else {
			fmt.Fprintf(&buf, "    <dc:identifier opf:scheme=\"ISBN\">%s</dc:identifier>\n", escapeXML(m.ISBN13))
		}
	}

	if identifiers != nil {
		for _, id := range identifiers {
			buf.WriteString("    <dc:identifier")
			if id.ID != "" {
				fmt.Fprintf(&buf, " id=\"%s\"", escapeXMLAttr(id.ID))
			}
			if id.Scheme != "" {
				fmt.Fprintf(&buf, " opf:scheme=\"%s\"", escapeXMLAttr(id.Scheme))
			}
			fmt.Fprintf(&buf, ">%s</dc:identifier>\n", escapeXML(id.Value))
		}
	} else {
		if uniqueIDAttr != "" {
			val := cmp.Or(m.Identifiers[uniqueIDAttr], m.Identifiers["uuid"], m.Identifiers["isbn"], m.ISBN13,
				"urn:uuid:01234567-89ab-cdef-0123-456789abcdef")
			fmt.Fprintf(&buf, "    <dc:identifier id=\"%s\">%s</dc:identifier>\n", escapeXMLAttr(uniqueIDAttr), escapeXML(val))
		}
		for k, v := range m.Identifiers {
			if k != uniqueIDAttr {
				fmt.Fprintf(&buf, "    <dc:identifier opf:scheme=\"%s\" id=\"id-%s\">%s</dc:identifier>\n", escapeXMLAttr(k), escapeXMLAttr(k), escapeXML(v))
			}
		}
	}

	// Series (EPUB 2 / Calibre & EPUB 3 belongs-to-collection)
	if m.Series != "" {
		buf.WriteString(fmt.Sprintf("    <meta name=\"calibre:series\" content=\"%s\"/>\n", escapeXMLAttr(m.Series)))
		if m.SeriesIndex != nil {
			buf.WriteString(fmt.Sprintf("    <meta name=\"calibre:series_index\" content=\"%s\"/>\n", formatFloat(*m.SeriesIndex)))
		}
		if isEPUB3 {
			fmt.Fprintf(&buf, "    <meta property=\"belongs-to-collection\" id=\"series-c01\">%s</meta>\n", escapeXML(m.Series))
			buf.WriteString("    <meta refines=\"#series-c01\" property=\"collection-type\">series</meta>\n")
			if m.SeriesIndex != nil {
				fmt.Fprintf(&buf, "    <meta refines=\"#series-c01\" property=\"group-position\">%s</meta>\n", formatFloat(*m.SeriesIndex))
			}
		}
	}

	// Title Sort
	if m.Title != "" {
		buf.WriteString(fmt.Sprintf("    <meta name=\"calibre:title_sort\" content=\"%s\"/>\n", escapeXMLAttr(m.Title)))
	}

	// Cover Meta
	if coverMetaID != "" {
		buf.WriteString(fmt.Sprintf("    <meta name=\"cover\" content=\"%s\"/>\n", escapeXMLAttr(coverMetaID)))
	}

	// Extra metadata tags
	hasModified := false
	for k, v := range m.ExtraMeta {
		if k == "dcterms:modified" {
			hasModified = true
			if isEPUB3 {
				buf.WriteString(fmt.Sprintf("    <meta property=\"dcterms:modified\">%s</meta>\n", escapeXML(v)))
				continue
			}
		}
		if strings.Contains(k, ":") {
			buf.WriteString(fmt.Sprintf("    <meta name=\"%s\" content=\"%s\"/>\n", escapeXMLAttr(k), escapeXMLAttr(v)))
		} else {
			buf.WriteString(fmt.Sprintf("    <meta property=\"%s\">%s</meta>\n", escapeXMLAttr(k), escapeXML(v)))
		}
	}

	// For EPUB 3, ensure dcterms:modified is present
	if isEPUB3 && !hasModified {
		nowStr := time.Now().UTC().Format("2006-01-02T15:04:05Z")
		buf.WriteString(fmt.Sprintf("    <meta property=\"dcterms:modified\">%s</meta>\n", nowStr))
	}

	buf.WriteString("  </metadata>")
	return buf.Bytes()
}

// replaceMetadataBlock swaps the existing <metadata> section with new XML bytes.
func replaceMetadataBlock(rawOPF []byte, newMetaBlock []byte) ([]byte, error) {
	loc := reMetadata.FindIndex(rawOPF)
	if loc == nil {
		return nil, fmt.Errorf("%w: <metadata> block not found in OPF", ErrMalformedOPF)
	}
	var res bytes.Buffer
	res.Write(rawOPF[:loc[0]])
	res.Write(newMetaBlock)
	res.Write(rawOPF[loc[1]:])
	return res.Bytes(), nil
}
