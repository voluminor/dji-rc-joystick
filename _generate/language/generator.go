package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

// // // // // // // // // //

const (
	fallbackKey = "en"
	formatVerbs = "vTtbcdoOxXUeEfFgGspq"
	nameKey     = "language.name"
)

type entryObj struct {
	key    string
	name   string
	values map[string]string
}

type catalogObj struct {
	entries []entryObj
}

// //

func validLanguageKey(value string) bool {
	if value == "" {
		return false
	}

	previousHyphen := true
	for index := 0; index < len(value); index++ {
		char := value[index]
		if char == '-' {
			if previousHyphen {
				return false
			}
			previousHyphen = true
			continue
		}
		if char < '0' || char > '9' {
			if char < 'A' || char > 'Z' {
				if char < 'a' || char > 'z' {
					return false
				}
			}
		}
		previousHyphen = false
	}

	return !previousHyphen
}

func decodeValues(fileName string, data []byte) (map[string]string, error) {
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("%s contains invalid UTF-8", fileName)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	tokenValue, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", fileName, err)
	}
	delimiter, ok := tokenValue.(json.Delim)
	if !ok || delimiter != '{' {
		return nil, fmt.Errorf("%s must contain a JSON object", fileName)
	}

	values := make(map[string]string)
	for decoder.More() {
		tokenValue, err = decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("decode key in %s: %w", fileName, err)
		}

		key, keyOK := tokenValue.(string)
		if !keyOK {
			return nil, fmt.Errorf("%s contains a non-string key", fileName)
		}
		if key == "" {
			return nil, fmt.Errorf("%s contains an empty key", fileName)
		}
		if _, exists := values[key]; exists {
			return nil, fmt.Errorf("%s contains duplicate key %q", fileName, key)
		}

		var value string
		if err = decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("decode value for key %q in %s: %w", key, fileName, err)
		}
		if value == "" {
			return nil, fmt.Errorf("%s contains empty value for key %q", fileName, key)
		}
		values[key] = value
	}

	if _, err = decoder.Token(); err != nil {
		return nil, fmt.Errorf("close JSON object in %s: %w", fileName, err)
	}

	var trailing any
	err = decoder.Decode(&trailing)
	if !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("%s contains data after the JSON object", fileName)
		}
		return nil, fmt.Errorf("decode trailing data in %s: %w", fileName, err)
	}

	return values, nil
}

func readEntry(path string, fileName string) (entryObj, error) {
	key := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	if !validLanguageKey(key) {
		return entryObj{}, fmt.Errorf("invalid language filename %q", fileName)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return entryObj{}, fmt.Errorf("read %s: %w", fileName, err)
	}
	values, err := decodeValues(fileName, data)
	if err != nil {
		return entryObj{}, err
	}

	name, ok := values[nameKey]
	if !ok {
		return entryObj{}, fmt.Errorf("%s is missing required key %q", fileName, nameKey)
	}
	if strings.TrimSpace(name) == "" {
		return entryObj{}, fmt.Errorf("%s contains empty value for key %q", fileName, nameKey)
	}

	return entryObj{
		key:    key,
		name:   name,
		values: values,
	}, nil
}

func compareKeys(entry entryObj, reference entryObj) error {
	missing := make([]string, 0)
	additional := make([]string, 0)

	for key := range reference.values {
		if _, ok := entry.values[key]; !ok {
			missing = append(missing, key)
		}
	}
	for key := range entry.values {
		if _, ok := reference.values[key]; !ok {
			additional = append(additional, key)
		}
	}
	if len(missing) == 0 && len(additional) == 0 {
		return nil
	}

	sort.Strings(missing)
	sort.Strings(additional)

	parts := make([]string, 0, 2)
	if len(missing) > 0 {
		parts = append(parts, "missing keys: "+strings.Join(missing, ", "))
	}
	if len(additional) > 0 {
		parts = append(parts, "additional keys: "+strings.Join(additional, ", "))
	}

	return fmt.Errorf("%s does not match %s: %s", entry.key+".json", reference.key+".json", strings.Join(parts, "; "))
}

func formatDirectives(value string) []string {
	directives := make([]string, 0, 2)
	for index := 0; index+1 < len(value); index++ {
		if value[index] != '%' {
			continue
		}

		verb := value[index+1]
		if verb == '%' {
			index++
			continue
		}
		if strings.IndexByte(formatVerbs, verb) < 0 {
			continue
		}

		directives = append(directives, value[index:index+2])
		index++
	}

	return directives
}

func compareFormatDirectives(entry entryObj, reference entryObj) error {
	mismatches := make([]string, 0)
	for _, key := range sortedKeys(reference.values) {
		value, ok := entry.values[key]
		if !ok {
			continue
		}

		want := formatDirectives(reference.values[key])
		got := formatDirectives(value)
		if slices.Equal(got, want) {
			continue
		}

		mismatches = append(mismatches, fmt.Sprintf(
			"key %q: got [%s], want [%s]",
			key,
			strings.Join(got, ", "),
			strings.Join(want, ", "),
		))
	}
	if len(mismatches) == 0 {
		return nil
	}

	return fmt.Errorf("%s has formatting directive mismatch: %s", entry.key+".json", strings.Join(mismatches, "; "))
}

func readCatalog(sourceDir string) (catalogObj, error) {
	directoryEntries, err := os.ReadDir(sourceDir)
	if err != nil {
		return catalogObj{}, fmt.Errorf("read translation directory: %w", err)
	}

	fileNames := make([]string, 0, len(directoryEntries))
	for _, directoryEntry := range directoryEntries {
		if directoryEntry.IsDir() || filepath.Ext(directoryEntry.Name()) != ".json" {
			continue
		}

		info, infoErr := directoryEntry.Info()
		if infoErr != nil {
			return catalogObj{}, fmt.Errorf("inspect %s: %w", directoryEntry.Name(), infoErr)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		fileNames = append(fileNames, directoryEntry.Name())
	}
	sort.Strings(fileNames)

	entries := make([]entryObj, 0, len(fileNames))
	referenceIndex := -1
	for _, fileName := range fileNames {
		entry, readErr := readEntry(filepath.Join(sourceDir, fileName), fileName)
		if readErr != nil {
			return catalogObj{}, readErr
		}
		if entry.key == fallbackKey {
			referenceIndex = len(entries)
		}
		entries = append(entries, entry)
	}
	if referenceIndex < 0 {
		return catalogObj{}, fmt.Errorf("%s.json is required", fallbackKey)
	}

	reference := entries[referenceIndex]
	mismatchErrors := make([]error, 0)
	for _, entry := range entries {
		if entry.key == fallbackKey {
			continue
		}
		if mismatchErr := compareKeys(entry, reference); mismatchErr != nil {
			mismatchErrors = append(mismatchErrors, mismatchErr)
		}
		if mismatchErr := compareFormatDirectives(entry, reference); mismatchErr != nil {
			mismatchErrors = append(mismatchErrors, mismatchErr)
		}
	}
	if len(mismatchErrors) > 0 {
		return catalogObj{}, errors.Join(mismatchErrors...)
	}

	return catalogObj{entries: entries}, nil
}
