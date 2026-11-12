// Command openapi-bundle resolves the external $ref tree under a root
// OpenAPI document and writes a single self-contained YAML to --output,
// suitable for downstream codegen (oapi-codegen, openapi-typescript,
// swift-openapi-generator) and for shipping as the openapi.yaml of a
// published package whose consumers cannot follow $refs to files outside
// the package.
//
// External refs that resolve to a schema that is also re-exported at
// the root by the same name are rewritten to internal
// `#/components/<category>/<name>` refs. External refs that don't match
// a root re-export are inlined in place. This preserves the
// deduplication that downstream type generators rely on to produce a
// single named type per schema.
package main

import (
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

func main() {
	root := flag.String("root", "", "path to the root OpenAPI spec to bundle")
	out := flag.String("output", "", "path to write the bundled spec to")
	flag.Parse()

	if *root == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: openapi-bundle --root <root.yaml> --output <bundled.yaml>")
		os.Exit(2)
	}

	if err := run(*root, *out); err != nil {
		fmt.Fprintln(os.Stderr, "openapi-bundle:", err)
		os.Exit(1)
	}
}

func run(rootPath, outPath string) error {
	b := newBundler()
	doc, err := b.load(rootPath)
	if err != nil {
		return err
	}

	b.collectExports(doc, rootPath)

	// Pass 1: expand the root's `components.<category>.<name>` placeholders
	// in place. These are pure `$ref` aliases pointing at the canonical
	// definition in an external file; rewriting them to internal refs would
	// produce self-references, so they get inlined instead.
	if err := b.inlineRootComponents(doc, rootPath); err != nil {
		return err
	}

	// Pass 2: walk anything else (paths, operations, nested schemas) and
	// rewrite cross-file refs to internal refs when the target matches a
	// root export; inline otherwise.
	if err := b.walk(doc, rootPath); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(outPath), err)
	}
	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", outPath, err)
	}
	defer f.Close()

	enc := yaml.NewEncoder(f)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("encode %s: %w", outPath, err)
	}
	return enc.Close()
}

type exportKey struct {
	category string // "schemas", "parameters", "responses", ...
	name     string
}

type bundler struct {
	files     map[string]*yaml.Node // absolute path -> root node
	exports   map[exportKey]bool    // names re-exported at the root's components
	resolving map[string]bool       // active absolute file + JSON pointer references
}

func newBundler() *bundler {
	return &bundler{
		files:     make(map[string]*yaml.Node),
		exports:   make(map[exportKey]bool),
		resolving: make(map[string]bool),
	}
}

// load reads and parses a YAML file, caching the result so repeated refs
// into the same file share one parsed tree.
func (b *bundler) load(path string) (*yaml.Node, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("abs %s: %w", path, err)
	}
	if cached, ok := b.files[abs]; ok {
		return cached, nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", abs, err)
	}
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, fmt.Errorf("parse %s: %w", abs, err)
	}
	// Unwrap the document node.
	doc := &node
	if doc.Kind == yaml.DocumentNode && len(doc.Content) == 1 {
		doc = doc.Content[0]
	}
	b.files[abs] = doc
	return doc, nil
}

// collectExports records the set of component names the root spec re-exports
// from external files. Anything in `components.<category>.<name>` whose only
// content is a `$ref` is treated as a re-export — cross-file refs to the
// same `<category>/<name>` will be rewritten to internal refs instead of
// inlined.
func (b *bundler) collectExports(root *yaml.Node, rootPath string) {
	components := mapValue(root, "components")
	if components == nil {
		return
	}
	for i := 0; i+1 < len(components.Content); i += 2 {
		category := components.Content[i].Value
		entries := components.Content[i+1]
		if entries.Kind != yaml.MappingNode {
			continue
		}
		for j := 0; j+1 < len(entries.Content); j += 2 {
			name := entries.Content[j].Value
			b.exports[exportKey{category: category, name: name}] = true
		}
	}
}

// inlineRootComponents walks `components.<category>.<name>` at the root
// of `doc` and, for each entry whose value is a placeholder mapping that
// only contains an external `$ref`, replaces it with the inlined target
// content. The inlined content is walked through `walk` so any nested
// cross-file refs are rewritten / inlined normally.
func (b *bundler) inlineRootComponents(doc *yaml.Node, rootPath string) error {
	components := mapValue(doc, "components")
	if components == nil {
		return nil
	}
	for i := 0; i+1 < len(components.Content); i += 2 {
		entries := components.Content[i+1]
		if entries.Kind != yaml.MappingNode {
			continue
		}
		for j := 0; j+1 < len(entries.Content); j += 2 {
			val := entries.Content[j+1]
			refVal, ok := isExternalRefPlaceholder(val)
			if !ok {
				continue
			}
			inlined, err := b.inlineRef(refVal, rootPath)
			if err != nil {
				return err
			}
			entries.Content[j+1] = inlined
		}
	}
	return nil
}

// isExternalRefPlaceholder returns the $ref value and true when `n` is a
// mapping whose only entry is `$ref: <external>`. Internal (`#/...`) refs
// don't count — they're already in their final form.
func isExternalRefPlaceholder(n *yaml.Node) (string, bool) {
	if n == nil || n.Kind != yaml.MappingNode || len(n.Content) != 2 {
		return "", false
	}
	if n.Content[0].Value != "$ref" || n.Content[1].Kind != yaml.ScalarNode {
		return "", false
	}
	v := n.Content[1].Value
	if strings.HasPrefix(v, "#") {
		return "", false
	}
	return v, true
}

// validateLocalRef rejects network and absolute-file references. Bundling is
// deliberately filesystem-local so generation never performs network access.
func validateLocalRef(refStr string) error {
	filePart, _, _ := strings.Cut(refStr, "#")
	parsed, err := url.Parse(filePart)
	if err != nil {
		return fmt.Errorf("invalid reference path: %w", err)
	}
	if parsed.IsAbs() || parsed.Host != "" || filepath.IsAbs(filePart) {
		return fmt.Errorf("only relative local file references are supported")
	}
	return nil
}

// inlineRef resolves an external ref string (relative to `srcPath`) to a
// deep-copied subtree with all of *its* cross-file refs already walked.
func (b *bundler) inlineRef(refStr, srcPath string) (*yaml.Node, error) {
	if err := validateLocalRef(refStr); err != nil {
		return nil, fmt.Errorf("ref %s: %w", refStr, err)
	}
	filePart, ptr, _ := strings.Cut(refStr, "#")
	if decoded, err := url.PathUnescape(ptr); err == nil {
		ptr = decoded
	}
	target, err := filepath.Abs(filepath.Join(filepath.Dir(srcPath), filePart))
	if err != nil {
		return nil, fmt.Errorf("ref %s: %w", refStr, err)
	}
	key := target + "#" + ptr
	if b.resolving[key] {
		return nil, fmt.Errorf("ref %s: cyclic external reference involving %s", refStr, key)
	}
	b.resolving[key] = true
	defer delete(b.resolving, key)

	targetDoc, err := b.load(target)
	if err != nil {
		return nil, fmt.Errorf("ref %s: %w", refStr, err)
	}
	resolved, err := resolvePointer(targetDoc, ptr)
	if err != nil {
		return nil, fmt.Errorf("ref %s: %w", refStr, err)
	}
	inlined := deepCopyNode(resolved)
	if err := b.walk(inlined, target); err != nil {
		return nil, err
	}
	return inlined, nil
}

// walk traverses `node`, rewriting external refs (relative to `srcPath`)
// into either internal refs (when the target matches a root re-export) or
// inlined content (otherwise). Internal refs are left untouched.
func (b *bundler) walk(node *yaml.Node, srcPath string) error {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range node.Content {
			if err := b.walk(c, srcPath); err != nil {
				return err
			}
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, val := node.Content[i], node.Content[i+1]
			if key.Value == "$ref" && val.Kind == yaml.ScalarNode {
				if err := b.resolveRef(node, val, srcPath); err != nil {
					return err
				}
				return nil
			}
			if err := b.walk(val, srcPath); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveRef handles a single `$ref` value. If the ref is internal (`#/...`),
// leaves it as-is. Otherwise, rewrites to an internal ref if the target
// matches a root re-export, or inlines the resolved subtree into `parent`
// (replacing the `$ref` map).
func (b *bundler) resolveRef(parent, refNode *yaml.Node, srcPath string) error {
	ref := refNode.Value
	if strings.HasPrefix(ref, "#") {
		return nil
	}
	if err := validateLocalRef(ref); err != nil {
		return fmt.Errorf("ref %s: %w", ref, err)
	}
	_, ptr, _ := strings.Cut(ref, "#")
	if decoded, err := url.PathUnescape(ptr); err == nil {
		ptr = decoded
	}

	// Try to rewrite to an internal ref if the target name matches a
	// root-exported component under the same category.
	if cat, name, ok := componentRef(ptr); ok && b.exports[exportKey{category: cat, name: name}] {
		refNode.Value = "#/components/" + cat + "/" + name
		return nil
	}

	// Otherwise, inline the resolved subtree into the parent mapping.
	inlined, err := b.inlineRef(ref, srcPath)
	if err != nil {
		return err
	}
	*parent = *inlined
	return nil
}

// componentRef parses a JSON-pointer fragment of the form
// `/components/<category>/<name>` and returns (category, name, true).
// Anything else returns ok=false.
func componentRef(ptr string) (category, name string, ok bool) {
	const prefix = "/components/"
	if !strings.HasPrefix(ptr, prefix) {
		return "", "", false
	}
	rest := ptr[len(prefix):]
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return "", "", false
	}
	cat := rest[:slash]
	n := rest[slash+1:]
	if cat == "" || n == "" || strings.ContainsRune(n, '/') {
		return "", "", false
	}
	return cat, n, true
}

// resolvePointer walks `node` following the JSON-pointer segments in `ptr`
// (per RFC 6901). An empty pointer returns the root node.
func resolvePointer(node *yaml.Node, ptr string) (*yaml.Node, error) {
	if ptr == "" || ptr == "/" {
		return node, nil
	}
	if !strings.HasPrefix(ptr, "/") {
		return nil, fmt.Errorf("invalid JSON pointer %q", ptr)
	}
	cur := node
	for _, raw := range strings.Split(ptr[1:], "/") {
		seg := strings.NewReplacer("~1", "/", "~0", "~").Replace(raw)
		if cur.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("pointer segment %q does not address a mapping", seg)
		}
		next := mapValue(cur, seg)
		if next == nil {
			return nil, fmt.Errorf("pointer segment %q not found", seg)
		}
		cur = next
	}
	return cur, nil
}

// mapValue returns the value node for `key` in a mapping, or nil.
func mapValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// deepCopyNode returns a deep copy of `n` so inlining one ref doesn't
// alias the cached parsed tree of the source file.
func deepCopyNode(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	cp := *n
	if len(n.Content) > 0 {
		cp.Content = make([]*yaml.Node, len(n.Content))
		for i, c := range n.Content {
			cp.Content[i] = deepCopyNode(c)
		}
	}
	// Anchor and alias bookkeeping doesn't survive structural copy in a
	// useful form — strip both so the bundled YAML round-trips standalone.
	cp.Anchor = ""
	cp.Alias = nil
	return &cp
}
