package data

import (
	"path/filepath"
	"testing"

	targetsettings "github.com/voluminor/dji-rc-joystick/target/settings"
)

// // // // // // // // // //

func TestObjSavesUIAndUpdatesLanguage(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "settings.json")
	obj, err := New([]string{"--config", configPath})
	if err != nil {
		t.Fatal(err)
	}

	ui := obj.UI()
	ui.Language = "ru-RU"
	ui.TextSize = 1.5
	ui.DarkMode = false
	if err = obj.SaveUI(ui); err != nil {
		t.Fatal(err)
	}

	if current := obj.UI(); current != ui {
		t.Fatalf("UI() = %#v, want %#v", current, ui)
	}
	if current := obj.Language().Current(); current != "ru-RU" {
		t.Fatalf("active language = %q, want ru-RU", current)
	}
	if current := obj.Logging(); current.File.Dir != "logs" {
		t.Fatalf("logging changed while saving UI: %#v", current)
	}
	if current := obj.ConfigPath(); current != configPath {
		t.Fatalf("ConfigPath() = %q, want %q", current, configPath)
	}
}

func TestObjRejectsUnsupportedLanguageWithoutSaving(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "settings.json")
	obj, err := New([]string{"--config", configPath})
	if err != nil {
		t.Fatal(err)
	}

	active := obj.UI()
	invalid := targetsettings.UiObj{
		TextSize: active.TextSize,
		DarkMode: active.DarkMode,
		Language: "unknown",
	}
	if err = obj.SaveUI(invalid); err == nil {
		t.Fatal("SaveUI() accepted an unsupported language")
	}
	if current := obj.UI(); current != active {
		t.Fatalf("UI changed after rejected save: %#v", current)
	}
}

func TestObjSavesAndReloadsControlMapping(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "settings.json")
	obj, err := New([]string{"--config", configPath})
	if err != nil {
		t.Fatal(err)
	}

	mapping := obj.Mapping()
	mapping.Axes.LeftStickH.Action = "axis_slider1"
	mapping.Axes.LeftStickH.Invert = !mapping.Axes.LeftStickH.Invert
	if err = obj.SaveMapping(mapping); err != nil {
		t.Fatal(err)
	}
	if current := obj.Mapping(); current != mapping {
		t.Fatalf("Mapping() = %#v, want %#v", current, mapping)
	}

	reloaded, err := New([]string{"--config", configPath})
	if err != nil {
		t.Fatal(err)
	}
	if current := reloaded.Mapping(); current != mapping {
		t.Fatalf("reloaded Mapping() = %#v, want %#v", current, mapping)
	}
}

func TestObjRejectsInvalidControlMappingWithoutSaving(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "settings.json")
	obj, err := New([]string{"--config", configPath})
	if err != nil {
		t.Fatal(err)
	}

	active := obj.Mapping()
	invalid := active
	invalid.Buttons.Rth = "axis_x"
	if err = obj.SaveMapping(invalid); err == nil {
		t.Fatal("SaveMapping() accepted an analog action for a button")
	}
	if current := obj.Mapping(); current != active {
		t.Fatalf("mapping changed after rejected save: %#v", current)
	}
}
