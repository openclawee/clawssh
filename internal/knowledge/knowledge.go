package knowledge

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Document struct {
	Name    string
	Content string
	lower   string
}

// Base is an in-memory local SOP knowledge base.
type Base struct {
	Docs []Document
}

// LoadLocal loads markdown/text docs from a directory recursively.
func LoadLocal(root string) (*Base, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		root = filepath.Join("docs", "ops")
	}
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return &Base{}, nil
		}
		return nil, err
	}
	var paths []string
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".md" || ext == ".txt" {
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Strings(paths)
	kb := &Base{Docs: make([]Document, 0, len(paths))}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		content := string(b)
		kb.Docs = append(kb.Docs, Document{
			Name:    filepath.Base(p),
			Content: content,
			lower:   strings.ToLower(content),
		})
	}
	return kb, nil
}

// ReferenceSOP returns a markdown block injected to system prompt.
func (b *Base) ReferenceSOP(input string) string {
	if b == nil || len(b.Docs) == 0 {
		return ""
	}
	keywords := matchedKeywords(input)
	if len(keywords) == 0 {
		return ""
	}
	type hit struct {
		doc   Document
		score int
	}
	hits := make([]hit, 0, len(b.Docs))
	for _, d := range b.Docs {
		score := 0
		for _, kw := range keywords {
			if strings.Contains(d.lower, kw) {
				score++
			}
		}
		if score > 0 {
			hits = append(hits, hit{doc: d, score: score})
		}
	}
	if len(hits) == 0 {
		return ""
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	if len(hits) > 3 {
		hits = hits[:3]
	}
	var sb strings.Builder
	sb.WriteString("## Reference SOP\n")
	for _, h := range hits {
		snippet := compactSnippet(h.doc.Content, 480)
		sb.WriteString(fmt.Sprintf("- [%s]\n%s\n", h.doc.Name, snippet))
	}
	return sb.String()
}

func matchedKeywords(input string) []string {
	text := strings.ToLower(strings.TrimSpace(input))
	if text == "" {
		return nil
	}
	// Initial keyword set for ops docs retrieval.
	candidates := []string{
		"oom", "mysql", "backup", "restore", "disk", "cpu", "memory", "redis", "nginx", "latency",
	}
	var out []string
	for _, c := range candidates {
		if strings.Contains(text, c) {
			out = append(out, c)
		}
	}
	return out
}

func compactSnippet(s string, max int) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	var kept []string
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		kept = append(kept, t)
		if len(strings.Join(kept, " ")) >= max {
			break
		}
	}
	out := strings.Join(kept, " ")
	if len(out) > max {
		out = out[:max] + "..."
	}
	return out
}
