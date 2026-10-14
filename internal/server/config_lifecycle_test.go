package server

import (
	"testing"

	"github.com/lbe/sfpg-go/web"
)

func TestParseConfigUITemplates_Coverage(t *testing.T) {
	t.Parallel()
	templates, err := parseConfigUITemplates(web.FS)
	if err != nil {
		t.Fatalf("parseConfigUITemplates failed: %v", err)
	}

	if templates.SaveRestartAlert == nil {
		t.Error("SaveRestartAlert template is nil")
	}
	if templates.SaveSuccessAlert == nil {
		t.Error("SaveSuccessAlert template is nil")
	}
	if templates.ExportModal == nil {
		t.Error("ExportModal template is nil")
	}
	if templates.ImportModal == nil {
		t.Error("ImportModal template is nil")
	}
	if templates.RestoreModal == nil {
		t.Error("RestoreModal template is nil")
	}
	if templates.RestoreSuccessAlert == nil {
		t.Error("RestoreSuccessAlert template is nil")
	}
	if templates.ImportSuccessAlert == nil {
		t.Error("ImportSuccessAlert template is nil")
	}
	if templates.RestartInitiatedAlert == nil {
		t.Error("RestartInitiatedAlert template is nil")
	}
}
