package main

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Validate YAML syntax and the supported book schema before changing projects.
// Decoding via a map also detects duplicate keys; unknown scalar metadata is
// retained rather than silently discarded on the next write.
func decodeBookYAML(data []byte) (*Book, error) {
	var fields map[string]yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&fields); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("book.yaml deve contenere un solo documento YAML")
	}
	b := &Book{Language: "it", Chapters: []Chapter{}, ExtraFields: make(map[string]string)}
	for key, node := range fields {
		if key == "chapters" {
			if node.Tag == "!!null" {
				continue
			}
			if node.Kind != yaml.SequenceNode {
				return nil, fmt.Errorf("chapters deve essere una lista")
			}
			for _, item := range node.Content {
				if item.Kind != yaml.MappingNode {
					return nil, fmt.Errorf("capitolo non valido")
				}
				var chapter map[string]string
				if err := item.Decode(&chapter); err != nil {
					return nil, err
				}
				if strings.TrimSpace(chapter["file"]) == "" {
					return nil, fmt.Errorf("capitolo privo di file")
				}
				b.Chapters = append(b.Chapters, Chapter{File: chapter["file"], Title: chapter["title"], ID: chapter["id"], Type: chapter["type"]})
			}
			continue
		}
		if node.Kind != yaml.ScalarNode {
			return nil, fmt.Errorf("il campo %s deve essere scalare", key)
		}
		value := node.Value
		switch key {
		case "title":
			b.Title = value
		case "author":
			b.Author = value
		case "language":
			b.Language = value
		case "uuid":
			b.UUID = value
		case "cover":
			b.Cover = value
		case "style":
			b.Style = value
		case "opening":
			b.Opening = value
		case "font":
			b.Font = value
		case "scenebreak":
			b.SceneBreak = value
		case "trim":
			b.Trim = value
		case "pages":
			if err := node.Decode(&b.Pages); err != nil {
				return nil, fmt.Errorf("pages non valido: %w", err)
			}
		case "bleed":
			switch strings.ToLower(value) {
			case "true", "yes", "1":
				b.Bleed = true
			case "false", "no", "0", "":
				b.Bleed = false
			default:
				return nil, fmt.Errorf("bleed non valido")
			}
		default:
			b.ExtraFields[key] = value
		}
	}
	if strings.TrimSpace(b.Title) == "" && len(b.Chapters) == 0 {
		return nil, fmt.Errorf("book.yaml privo di titolo o capitoli")
	}
	return b, nil
}

// Keep simple values unchanged for CLI compatibility, but quote values whose
// punctuation/newlines would otherwise turn user-entered titles into invalid YAML.
func yamlScalar(value string) string {
	data, _ := yaml.Marshal(value)
	encoded := strings.TrimSpace(string(data))
	if encoded == value && !strings.ContainsAny(value, "\n\r\t") {
		return value
	}
	return strconv.Quote(value)
}
