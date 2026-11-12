package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func writeFixture(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func readDocument(t *testing.T, path string) *yaml.Node {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Content[0]
}

func TestBundleSharedComponentAliasPreservesNamedReference(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "public.yaml", `openapi: 3.0.3
info: {title: Public, version: 1.0.0}
paths: {}
components:
  schemas:
    Request:
      type: object
      properties:
        id: {type: string}
`)
	writeFixture(t, dir, "root.yaml", `openapi: 3.0.3
info: {title: Example, version: 1.0.0}
paths:
  /requests:
    get:
      operationId: listRequests
      responses:
        '200':
          description: ok
          content:
            application/json:
              schema:
                type: array
                items:
                  $ref: 'public.yaml#/components/schemas/Request'
components:
  schemas:
    Request:
      $ref: 'public.yaml#/components/schemas/Request'
`)
	output := filepath.Join(dir, "bundled.yaml")
	if err := run(filepath.Join(dir, "root.yaml"), output); err != nil {
		t.Fatal(err)
	}
	root := readDocument(t, output)
	items, err := resolvePointer(root, "/paths/~1requests/get/responses/200/content/application~1json/schema/items")
	if err != nil {
		t.Fatal(err)
	}
	ref := mapValue(items, "$ref")
	if ref == nil || ref.Value != "#/components/schemas/Request" {
		t.Fatal("shared named type was inlined instead of referenced")
	}
	schema, err := resolvePointer(root, "/components/schemas/Request")
	if err != nil {
		t.Fatal(err)
	}
	if mapValue(schema, "$ref") != nil || mapValue(schema, "properties") == nil {
		t.Fatal("export did not resolve to its single authoritative definition")
	}
}

func TestBundleRecursivelyResolvesNestedExternalReferences(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "address.yaml", `components:
  schemas:
    Address:
      type: object
      properties:
        city: {type: string}
`)
	writeFixture(t, dir, "request.yaml", `components:
  schemas:
    Request:
      type: object
      properties:
        address:
          $ref: 'address.yaml#/components/schemas/Address'
`)
	writeFixture(t, dir, "root.yaml", `openapi: 3.0.3
info: {title: Example, version: 1.0.0}
paths: {}
components:
  schemas:
    Request:
      $ref: 'request.yaml#/components/schemas/Request'
`)
	output := filepath.Join(dir, "bundled.yaml")
	if err := run(filepath.Join(dir, "root.yaml"), output); err != nil {
		t.Fatal(err)
	}
	root := readDocument(t, output)
	address, err := resolvePointer(root, "/components/schemas/Request/properties/address")
	if err != nil {
		t.Fatal(err)
	}
	if mapValue(address, "$ref") != nil || mapValue(address, "properties") == nil {
		t.Fatal("nested external schema was not recursively inlined")
	}
}

func TestBundleOutputIsDeterministicAndDoesNotMutateCachedSources(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "shared.yaml", `components:
  schemas:
    Identifier:
      type: string
    Record:
      type: object
      properties:
        id:
          $ref: '#/components/schemas/Identifier'
`)
	writeFixture(t, dir, "root.yaml", `openapi: 3.0.3
info: {title: Example, version: 1.0.0}
paths:
  /first:
    $ref: 'shared.yaml#/components/schemas/Record'
  /second:
    $ref: 'shared.yaml#/components/schemas/Record'
components:
  schemas:
    Record:
      $ref: 'shared.yaml#/components/schemas/Record'
`)
	root := filepath.Join(dir, "root.yaml")
	first := filepath.Join(dir, "first.yaml")
	second := filepath.Join(dir, "second.yaml")
	if err := run(root, first); err != nil {
		t.Fatal(err)
	}
	if err := run(root, second); err != nil {
		t.Fatal(err)
	}
	firstBytes, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("identical inputs produced different bundles")
	}
}

func TestBundlePreservesRecursiveRootComponent(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "node.yaml", `components:
  schemas:
    Node:
      type: object
      properties:
        next:
          $ref: 'node.yaml#/components/schemas/Node'
`)
	writeFixture(t, dir, "root.yaml", `openapi: 3.0.3
info: {title: Example, version: 1.0.0}
paths: {}
components:
  schemas:
    Node:
      $ref: 'node.yaml#/components/schemas/Node'
`)
	output := filepath.Join(dir, "bundled.yaml")
	if err := run(filepath.Join(dir, "root.yaml"), output); err != nil {
		t.Fatal(err)
	}
	root := readDocument(t, output)
	next, err := resolvePointer(root, "/components/schemas/Node/properties/next")
	if err != nil {
		t.Fatal(err)
	}
	if ref := mapValue(next, "$ref"); ref == nil || ref.Value != "#/components/schemas/Node" {
		t.Fatal("recursive named component did not retain its internal reference")
	}
}

func TestBundleRejectsReferenceCycles(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "node.yaml", `components:
  schemas:
    Node:
      type: object
      properties:
        next:
          $ref: 'node.yaml#/components/schemas/Node'
`)
	writeFixture(t, dir, "root.yaml", `openapi: 3.0.3
info: {title: Example, version: 1.0.0}
paths:
  /node:
    $ref: 'node.yaml#/components/schemas/Node'
`)
	err := run(filepath.Join(dir, "root.yaml"), filepath.Join(dir, "bundled.yaml"))
	if err == nil || !strings.Contains(err.Error(), "cyclic external reference") {
		t.Fatalf("expected cycle diagnostic, got %v", err)
	}
}

func TestBundleRejectsMalformedInputs(t *testing.T) {
	tests := []struct {
		name     string
		root     string
		external map[string]string
		want     string
	}{
		{name: "invalid YAML", root: "openapi: [", want: "parse"},
		{name: "missing external file", root: "openapi: 3.0.3\npaths:\n  /value:\n    $ref: 'missing.yaml#/value'\n", want: "read"},
		{name: "invalid JSON pointer", root: "openapi: 3.0.3\npaths:\n  /value:\n    $ref: 'other.yaml#value'\n", external: map[string]string{"other.yaml": "value: string\n"}, want: "invalid JSON pointer"},
		{name: "remote reference", root: "openapi: 3.0.3\npaths:\n  /value:\n    $ref: 'https://example.com/schema.yaml#/value'\n", want: "only relative local file references"},
		{name: "absolute file reference", root: "openapi: 3.0.3\npaths:\n  /value:\n    $ref: '/tmp/schema.yaml#/value'\n", want: "only relative local file references"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFixture(t, dir, "root.yaml", test.root)
			for name, body := range test.external {
				writeFixture(t, dir, name, body)
			}
			err := run(filepath.Join(dir, "root.yaml"), filepath.Join(dir, "bundled.yaml"))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected error containing %q, got %v", test.want, err)
			}
		})
	}
}
