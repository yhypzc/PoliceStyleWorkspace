package handlers

import (
	"path/filepath"
	"testing"
)

func TestSafeAppealRecordKey(t *testing.T) {
	valid := []string{"abcd1234_single", "xd_0123456789abcdef_school", "record.01"}
	for _, key := range valid {
		if !safeAppealRecordKey(key) {
			t.Errorf("expected valid record key %q", key)
		}
	}

	invalid := []string{"", ".", "..", "../record", `..\\record`, "/tmp/record", `C:\\temp\\record`, "record/name", "record name"}
	for _, key := range invalid {
		if safeAppealRecordKey(key) {
			t.Errorf("expected invalid record key %q", key)
		}
	}
}

func TestSafeAppealPhotoPathStaysInEvidenceDirectory(t *testing.T) {
	root := t.TempDir()
	dir, path, err := safeAppealPhotoPath(root, "record_01", "photo.png")
	if err != nil {
		t.Fatalf("safeAppealPhotoPath returned error: %v", err)
	}
	if filepath.Dir(path) != dir || !pathWithin(root, path) {
		t.Fatalf("photo path escaped evidence directory: %q", path)
	}

	for _, name := range []string{"../photo.png", `..\\photo.png`, "/tmp/photo.png", `C:\\temp\\photo.png`, "photo.txt"} {
		if _, _, err := safeAppealPhotoPath(root, "record_01", name); err == nil {
			t.Errorf("expected invalid photo path %q", name)
		}
	}
}
