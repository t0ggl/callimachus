package epub

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// generateSidecarXML produces Calibre-compatible companion XML sidecar content.
func generateSidecarXML(m Metadata, targetOpfPath string) []byte {
	var buf bytes.Buffer
	buf.WriteString("<?xml version='1.0' encoding='utf-8'?>\n")

	uniqueID := "uuid_id"
	if _, ok := m.Identifiers["calibre"]; ok {
		uniqueID = "calibre_id"
	}

	buf.WriteString(fmt.Sprintf("<package xmlns=\"http://www.idpf.org/2007/opf\" unique-identifier=\"%s\" version=\"2.0\">\n", uniqueID))
	buf.WriteString("    <metadata xmlns:dc=\"http://purl.org/dc/elements/1.1/\" xmlns:opf=\"http://www.idpf.org/2007/opf\">\n")

	// Identifiers
	if v, ok := m.Identifiers["calibre"]; ok {
		buf.WriteString(fmt.Sprintf("        <dc:identifier opf:scheme=\"calibre\" id=\"calibre_id\">%s</dc:identifier>\n", escapeXML(v)))
	}
	if v, ok := m.Identifiers["uuid"]; ok {
		buf.WriteString(fmt.Sprintf("        <dc:identifier opf:scheme=\"uuid\" id=\"uuid_id\">%s</dc:identifier>\n", escapeXML(v)))
	} else if uniqueID == "uuid_id" {
		buf.WriteString("        <dc:identifier opf:scheme=\"uuid\" id=\"uuid_id\">urn:uuid:01234567-89ab-cdef-0123-456789abcdef</dc:identifier>\n")
	}

	if m.ISBN13 != "" {
		buf.WriteString(fmt.Sprintf("        <dc:identifier opf:scheme=\"ISBN\">%s</dc:identifier>\n", escapeXML(m.ISBN13)))
	}

	for k, v := range m.Identifiers {
		if k == "calibre" || k == "uuid" || k == "isbn" || k == "calibre_id" || k == "uuid_id" {
			continue
		}
		buf.WriteString(fmt.Sprintf("        <dc:identifier opf:scheme=\"%s\">%s</dc:identifier>\n", escapeXMLAttr(k), escapeXML(v)))
	}

	// Title
	if m.Title != "" {
		buf.WriteString(fmt.Sprintf("        <dc:title>%s</dc:title>\n", escapeXML(m.Title)))
	}

	// Creators
	for i, author := range m.Authors {
		if i == 0 && m.SortAuthor != "" {
			buf.WriteString(fmt.Sprintf("        <dc:creator opf:file-as=\"%s\" opf:role=\"aut\">%s</dc:creator>\n", escapeXMLAttr(m.SortAuthor), escapeXML(author)))
		} else {
			buf.WriteString(fmt.Sprintf("        <dc:creator opf:role=\"aut\">%s</dc:creator>\n", escapeXML(author)))
		}
	}

	// Calibre contributor attribution
	buf.WriteString("        <dc:contributor opf:file-as=\"calibre\" opf:role=\"bkp\">calibre (7.23.0) [https://calibre-ebook.com]</dc:contributor>\n")

	// Dates and descriptions
	if m.PublishDate != "" {
		buf.WriteString(fmt.Sprintf("        <dc:date>%s</dc:date>\n", escapeXML(m.PublishDate)))
	}
	if m.Description != "" {
		buf.WriteString(fmt.Sprintf("        <dc:description>%s</dc:description>\n", escapeXML(m.Description)))
	}
	if m.Publisher != "" {
		buf.WriteString(fmt.Sprintf("        <dc:publisher>%s</dc:publisher>\n", escapeXML(m.Publisher)))
	}

	lang := m.Language
	if lang == "" {
		lang = "en"
	}
	buf.WriteString(fmt.Sprintf("        <dc:language>%s</dc:language>\n", escapeXML(lang)))

	// Calibre series metadata
	if m.Series != "" {
		buf.WriteString(fmt.Sprintf("        <meta name=\"calibre:series\" content=\"%s\"/>\n", escapeXMLAttr(m.Series)))
		if m.SeriesIndex != nil {
			buf.WriteString(fmt.Sprintf("        <meta name=\"calibre:series_index\" content=\"%s\"/>\n", formatFloat(*m.SeriesIndex)))
		}
	}

	if m.Title != "" {
		buf.WriteString(fmt.Sprintf("        <meta name=\"calibre:title_sort\" content=\"%s\"/>\n", escapeXMLAttr(m.Title)))
	}

	buf.WriteString("    </metadata>\n")

	// Companion cover reference in guide
	baseName := strings.TrimSuffix(filepath.Base(targetOpfPath), filepath.Ext(targetOpfPath))
	coverName := baseName + ".jpg"
	buf.WriteString("    <guide>\n")
	buf.WriteString(fmt.Sprintf("        <reference type=\"cover\" title=\"Cover\" href=\"%s\"/>\n", escapeXMLAttr(coverName)))
	buf.WriteString("    </guide>\n")
	buf.WriteString("</package>\n")

	return buf.Bytes()
}

// GenerateSidecarXML produces Calibre-compatible companion XML sidecar content.
func GenerateSidecarXML(m Metadata, targetOpfPath string) []byte {
	return generateSidecarXML(m, targetOpfPath)
}

// WriteSidecarFile writes the Calibre-compatible companion XML sidecar file.
func WriteSidecarFile(m Metadata, targetOpfPath string) error {
	return writeSidecarFile(m, targetOpfPath)
}

// writeSidecarFile writes the Calibre-compatible companion XML sidecar file.
func writeSidecarFile(m Metadata, targetOpfPath string) error {
	targetDir := filepath.Dir(targetOpfPath)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory for sidecar %s: %w", targetDir, err)
	}

	data := generateSidecarXML(m, targetOpfPath)
	return os.WriteFile(targetOpfPath, data, 0644)
}
