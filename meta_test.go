package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The single metadata writer must reproduce the historical string-patched
// [metadata] sections byte for byte. Goldens are real task.toml files from
// public suites (spire, gin), committed unmodified.
func TestTaskTOMLWriterByteIdentity(t *testing.T) {
	cases := []struct {
		note, golden string
		meta         Meta
	}{
		{
			note:   "reverified public instance (spire shape)",
			golden: "testdata/golden/tomlwriter/spire-task.toml",
			meta:   Meta{ContainerVerified: true, OriginVisibility: "public", InstructionClass: "change-spec"},
		},
		{
			note:   "host-only classified instance (gin shape)",
			golden: "testdata/golden/tomlwriter/gin-task.toml",
			meta:   Meta{InstructionClass: "change-spec"},
		},
	}
	for _, c := range cases {
		want, err := os.ReadFile(c.golden)
		if err != nil {
			t.Fatalf("%s: %v", c.note, err)
		}
		got := applyTaskTOMLMetadata(string(want), c.meta)
		if !bytes.Equal([]byte(got), want) {
			t.Errorf("%s: writer output differs from golden\n--- got ---\n%s\n--- want ---\n%s", c.note, got, want)
		}
	}
}

func TestTOMLMetadataSectionShapes(t *testing.T) {
	cases := []struct {
		note string
		meta Meta
		want string
	}{
		{
			note: "fresh emit",
			meta: Meta{InstanceID: "fix-pr1-a"},
			want: "[metadata]\nverified_host_only = true\ninstance_id = \"fix-pr1-a\"\n",
		},
		{
			note: "reverified discard keeps origin, no container flag",
			meta: Meta{InstanceID: "fix-pr1-a", OriginVisibility: "private"},
			want: "[metadata]\norigin_visibility = \"private\"\ninstance_id = \"fix-pr1-a\"\n",
		},
		{
			note: "full pipeline",
			meta: Meta{InstanceID: "fix-pr1-a", Cluster: "c03", ContainerVerified: true, OriginVisibility: "public", InstructionClass: "bug-report"},
			want: "[metadata]\ncontainer_verified = true\norigin_visibility = \"public\"\ninstruction_class = \"bug-report\"\ninstance_id = \"fix-pr1-a\"\ncluster = \"c03\"\n",
		},
	}
	for _, c := range cases {
		if got := tomlMetadataSection(c.meta); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.note, got, c.want)
		}
	}
}

// copyDir copies a testdata tree into a temp root so mutating commands can
// run against it.
func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		sp, dp := filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())
		if e.IsDir() {
			copyDir(t, sp, dp)
			continue
		}
		data, err := os.ReadFile(sp)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dp, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestMigrateSchema1 lifts a committed public-suite schema-1 sample to
// schema 2 and proves the command is idempotent.
func TestMigrateSchema1(t *testing.T) {
	root := t.TempDir()
	copyDir(t, filepath.Join("testdata", "migrate"), root)
	if err := runMigrate(root); err != nil {
		t.Fatal(err)
	}

	inst := "fix-pr2169-fix-binding-empty-value-error-2169"
	dir := filepath.Join(root, inst)
	m, err := loadMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Schema != 2 || m.InstanceID != inst {
		t.Errorf("schema = %d, instance_id = %q", m.Schema, m.InstanceID)
	}
	if m.Kind != "fix" || m.PR != 2169 || m.Cluster != "c01" || m.InstructionClass != "change-spec" {
		t.Errorf("lifted fields wrong: %+v", m)
	}
	if len(m.TestFiles) != 1 || m.TestFiles[0] != "binding/form_mapping_test.go" {
		t.Errorf("test_files = %v", m.TestFiles)
	}
	toml, err := os.ReadFile(filepath.Join(dir, "task.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"instance_id = \"" + inst + "\"", "cluster = \"c01\"", "verified_host_only = true"} {
		if !bytes.Contains(toml, []byte(want)) {
			t.Errorf("task.toml missing %q after migrate", want)
		}
	}

	// idempotency: a second run must change no bytes
	before := dirManifest(t, root)
	if err := runMigrate(root); err != nil {
		t.Fatal(err)
	}
	if after := dirManifest(t, root); after != before {
		t.Errorf("second migrate changed bytes:\n%s\nvs\n%s", before, after)
	}
}
