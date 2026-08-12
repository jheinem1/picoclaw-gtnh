package greggpttools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestItemIDLookupUsesRuntimeIndexSlug(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, DefaultItemIndexPath)
	writeReferenceTestFile(t, path, "slug\tdisplay_name\treg_name\tname\n"+
		"1d0\tLegacy \"quoted\" item\tlegacy:item\titem.legacy\n"+
		"8852d0\tPotin Screw\tmiscutils:itemScrewPotin\titem.itemScrewPotin\n")
	cfg := DefaultConfig()
	cfg.Workspace = workspace
	registry := testRegistry(t, cfg)
	result, err := registry.Execute(context.Background(), "item_id_lookup", json.RawMessage(`{"id":8852,"damage":0}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.OK || !strings.Contains(result.Stdout, `"display_name":"Potin Screw"`) || !strings.Contains(result.Stdout, `"registry_name":"miscutils:itemScrewPotin"`) {
		t.Fatalf("unexpected lookup result: %+v", result)
	}
}

func TestModReferenceSearchPrefersMatchingSubject(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, DefaultModReferencePath, "binnie-botany.tsv")
	writeReferenceTestFile(t, path, "mod_id\tversion\tartifact\tartifact_sha256\tsource\tsubject\tcontent\n"+
		"binnie-botany\t2.6.32\tbinnie-mods-2.6.32.jar\tdeadbeef\tEnumFlowerColor.setupMutations\tAQUAMARINE\tKHAKI + CYAN -> AQUAMARINE (90%); WHITE + PALE_GREEN -> AQUAMARINE (100%)\n"+
		"binnie-botany\t2.6.32\tbinnie-mods-2.6.32.jar\tdeadbeef\tEnumFlowerColor.setupMutations\tSTEEL_BLUE\tBLUE + AQUAMARINE -> STEEL_BLUE (55%)\n")
	cfg := DefaultConfig()
	cfg.Workspace = workspace
	registry := testRegistry(t, cfg)
	result, err := registry.Execute(context.Background(), "mod_reference_search", json.RawMessage(`{"query":"how to get a flower with the Aquamarine trait","mod":"botany","limit":1}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.OK || !strings.Contains(result.Stdout, `"subject":"AQUAMARINE"`) || !strings.Contains(result.Stdout, "KHAKI + CYAN") {
		t.Fatalf("unexpected search result: %+v", result)
	}
	if strings.Contains(result.Stdout, `"subject":"STEEL_BLUE"`) {
		t.Fatalf("lower-ranked input-only match leaked into limited result: %s", result.Stdout)
	}
}

func writeReferenceTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)

	}
}
func TestModReferenceSearchResolvesThaumcraftAutocrafting(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, DefaultModReferencePath, "thaumic-energistics.tsv")
	writeReferenceTestFile(t, path, "mod_id\tversion\tartifact\tartifact_sha256\tsource\tsubject\tcontent\n"+
		"thaumicenergistics\t1.7.53-GTNH\tthaumicenergistics-1.7.53-GTNH.jar\t9e8e8315\tTileArcaneAssembler\tThaumcraft AE arcane autocrafting - Arcane Assembler pattern storage\tKnowledge Core stores arcane recipes encoded by the Knowledge Inscriber; a Vis Relay Interface replenishes the assembler.\n"+
		"thaumicenergistics\t1.7.53-GTNH\tthaumicenergistics-1.7.53-GTNH.jar\t9e8e8315\tTileEssentiaProvider\tThaumcraft AE essentia transport\tConnect the Essentia Provider to the ME network and construct.\n")
	cfg := DefaultConfig()
	cfg.Workspace = workspace
	registry := testRegistry(t, cfg)
	result, err := registry.Execute(context.Background(), "mod_reference_search", json.RawMessage(`{"query":"what do we need for Thaumcraft autocrafting in our ME system","limit":1}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	for _, want := range []string{"Arcane Assembler pattern storage", "Knowledge Core", "Vis Relay Interface", "1.7.53-GTNH"} {
		if !result.OK || !strings.Contains(result.Stdout, want) {
			t.Fatalf("result missing %q: %+v", want, result)
		}
	}
}
