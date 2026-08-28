package provider

import "testing"

// Codex publishes more than it shows: the entries it hides from its own picker
// must not be offered on a card either, and its ordering is the one to keep.
func TestParseCodexCatalogueKeepsListedModelsInOrder(t *testing.T) {
	output := []byte(`{"models":[
	  {"slug":"gpt-5.4","display_name":"GPT-5.4","visibility":"list","priority":16},
	  {"slug":"gpt-reserve","display_name":"GPT-Reserve","visibility":"hide","priority":3},
	  {"slug":"gpt-5.6-sol","display_name":"GPT-5.6-Sol","visibility":"list","priority":1}
	]}`)
	models := parseCodexCatalogue(output)
	if len(models) != 2 {
		t.Fatalf("models = %+v, want the two listed ones", models)
	}
	if models[0].ID != "gpt-5.6-sol" || models[0].Label != "GPT-5.6-Sol" {
		t.Fatalf("first model = %+v, want the highest priority one", models[0])
	}
	if models[1].ID != "gpt-5.4" {
		t.Fatalf("second model = %+v", models[1])
	}
}

// A catalogue that cannot be read is an empty list, never a nil the API would
// serialise as null.
func TestParseCodexCatalogueSurvivesGarbage(t *testing.T) {
	if models := parseCodexCatalogue([]byte("not json")); models == nil || len(models) != 0 {
		t.Fatalf("models = %+v, want an empty list", models)
	}
}

// agy writes a progress line before the table, and that line is not a model.
func TestParseAntigravityModelsSkipsTheProgressLine(t *testing.T) {
	output := "Fetching available models...\n" +
		"gemini-3.7-flash-high\tGemini 3.7 Flash (High)\n" +
		"claude-sonnet-4-6\tClaude Sonnet 4.6 (Thinking)\n"
	models := parseAntigravityModels(output)
	if len(models) != 2 {
		t.Fatalf("models = %+v, want two", models)
	}
	if models[0].ID != "gemini-3.7-flash-high" || models[0].Label != "Gemini 3.7 Flash (High)" {
		t.Fatalf("first model = %+v", models[0])
	}
}

// The header row of ollama's table is not a model, and only the first column is.
func TestParseOllamaListDropsTheHeader(t *testing.T) {
	output := "NAME              ID              SIZE      MODIFIED\n" +
		"qwen3:4b          abc123          2.6 GB    2 days ago\n" +
		"llama3.2:latest   def456          2.0 GB    3 weeks ago\n"
	models := parseOllamaList(output)
	if len(models) != 2 {
		t.Fatalf("models = %+v, want two", models)
	}
	if models[0].ID != "qwen3:4b" || models[1].ID != "llama3.2:latest" {
		t.Fatalf("models = %+v", models)
	}
}
