package mcp_test

import (
	"os"
	"os/exec"
	"testing"
	"time"

	capmcp "github.com/gracegaoya/ai-operations-copilot/internal/mcp"
)

// TestStdioListerPagination 验证 stdio lister 能正确处理外部 MCP 服务器的分页
// tools/list 响应。mockMCPServer 作为子进程运行，返回 3 页工具（每页 2 个）。
func TestStdioListerPagination(t *testing.T) {
	mockServer := buildMockServer(t, false)

	lister := capmcp.NewStdioListerWithTimeout(5*time.Second, 5*time.Second)
	config := capmcp.MCPServerConfig{
		Name:    "paginated-server",
		Command: mockServer,
	}

	tools, err := lister.List(t.Context(), config)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tools) != 6 {
		t.Fatalf("got %d tools, want 6", len(tools))
	}
	expected := []string{"tool.page1.a", "tool.page1.b", "tool.page2.a", "tool.page2.b", "tool.page3.a", "tool.page3.b"}
	for i, want := range expected {
		if tools[i].Name != want {
			t.Errorf("tool[%d].Name = %q, want %q", i, tools[i].Name, want)
		}
	}
}

// TestStdioListerSinglePage 验证不分页的服务器（nextCursor 为空）也能正常工作。
func TestStdioListerSinglePage(t *testing.T) {
	mockServer := buildMockServer(t, true)

	lister := capmcp.NewStdioListerWithTimeout(5*time.Second, 5*time.Second)
	config := capmcp.MCPServerConfig{
		Name:    "single-page-server",
		Command: mockServer,
	}

	tools, err := lister.List(t.Context(), config)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(tools))
	}
}

func buildMockServer(t *testing.T, singlePage bool) string {
	t.Helper()
	tmp := t.TempDir()
	src := tmp + "/mock_server.go"

	mode := "paged"
	if singlePage {
		mode = "single"
	}
	source := "package main\n\n" +
		"import (\n" +
		"\t\"bufio\"\n" +
		"\t\"encoding/json\"\n" +
		"\t\"os\"\n" +
		"\t\"strings\"\n" +
		")\n\n" +
		"type mockTool struct {\n" +
		"\tName        string         " + "`json:\"name\"`" + "\n" +
		"\tDescription string         " + "`json:\"description\"`" + "\n" +
		"\tInputSchema map[string]any " + "`json:\"inputSchema\"`" + "\n" +
		"}\n\n" +
		"type mockToolsListResult struct {\n" +
		"\tTools      []mockTool " + "`json:\"tools\"`" + "\n" +
		"\tNextCursor string     " + "`json:\"nextCursor,omitempty\"`" + "\n" +
		"}\n\n" +
		"func main() {\n" +
		"\tscanner := bufio.NewScanner(os.Stdin)\n" +
		"\tscanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)\n" +
		"\twriter := json.NewEncoder(os.Stdout)\n\n"

	if mode == "single" {
		source += "\tsinglePageTools := []mockTool{\n" +
			"\t\t{Name: \"single.tool1\", Description: \"tool 1\"},\n" +
			"\t\t{Name: \"single.tool2\", Description: \"tool 2\"},\n" +
			"\t}\n\n"
	} else {
		source += "\tpages := map[string]mockToolsListResult{\n" +
			"\t\t\"\": {\n" +
			"\t\t\tTools: []mockTool{\n" +
			"\t\t\t\t{Name: \"tool.page1.a\", Description: \"page1 tool a\"},\n" +
			"\t\t\t\t{Name: \"tool.page1.b\", Description: \"page1 tool b\"},\n" +
			"\t\t\t},\n" +
			"\t\t\tNextCursor: \"page2\",\n" +
			"\t\t},\n" +
			"\t\t\"page2\": {\n" +
			"\t\t\tTools: []mockTool{\n" +
			"\t\t\t\t{Name: \"tool.page2.a\", Description: \"page2 tool a\"},\n" +
			"\t\t\t\t{Name: \"tool.page2.b\", Description: \"page2 tool b\"},\n" +
			"\t\t\t},\n" +
			"\t\t\tNextCursor: \"page3\",\n" +
			"\t\t},\n" +
			"\t\t\"page3\": {\n" +
			"\t\t\tTools: []mockTool{\n" +
			"\t\t\t\t{Name: \"tool.page3.a\", Description: \"page3 tool a\"},\n" +
			"\t\t\t\t{Name: \"tool.page3.b\", Description: \"page3 tool b\"},\n" +
			"\t\t\t},\n" +
			"\t\t},\n" +
			"\t}\n\n"
	}

	source += "\tfor scanner.Scan() {\n" +
		"\t\tline := scanner.Bytes()\n" +
		"\t\tif len(strings.TrimSpace(string(line))) == 0 {\n" +
		"\t\t\tcontinue\n" +
		"\t\t}\n" +
		"\t\tvar req struct {\n" +
		"\t\t\tJSONRPC string " + "`json:\"jsonrpc\"`" + "\n" +
		"\t\t\tID      int    " + "`json:\"id\"`" + "\n" +
		"\t\t\tMethod  string " + "`json:\"method\"`" + "\n" +
		"\t\t\tParams  struct {\n" +
		"\t\t\t\tCursor string " + "`json:\"cursor,omitempty\"`" + "\n" +
		"\t\t\t} " + "`json:\"params\"`" + "\n" +
		"\t\t}\n" +
		"\t\tif err := json.Unmarshal(line, &req); err != nil {\n" +
		"\t\t\tcontinue\n" +
		"\t\t}\n\n" +
		"\t\tswitch req.Method {\n" +
		"\t\tcase \"initialize\":\n" +
		"\t\t\twriter.Encode(map[string]any{\n" +
		"\t\t\t\t\"jsonrpc\": \"2.0\",\n" +
		"\t\t\t\t\"id\":      req.ID,\n" +
		"\t\t\t\t\"result\": map[string]any{\n" +
		"\t\t\t\t\t\"protocolVersion\": \"2024-11-05\",\n" +
		"\t\t\t\t\t\"capabilities\":   map[string]any{},\n" +
		"\t\t\t\t\t\"serverInfo\":     map[string]any{\"name\": \"mock\", \"version\": \"0.0.1\"},\n" +
		"\t\t\t\t},\n" +
		"\t\t\t})\n" +
		"\t\tcase \"notifications/initialized\":\n"

	if mode == "single" {
		source += "\t\tcase \"tools/list\":\n" +
			"\t\t\twriter.Encode(map[string]any{\n" +
			"\t\t\t\t\"jsonrpc\": \"2.0\",\n" +
			"\t\t\t\t\"id\":      req.ID,\n" +
			"\t\t\t\t\"result\": map[string]any{\n" +
			"\t\t\t\t\t\"tools\": singlePageTools,\n" +
			"\t\t\t\t},\n" +
			"\t\t\t})\n"
	} else {
		source += "\t\tcase \"tools/list\":\n" +
			"\t\t\tcursor := req.Params.Cursor\n" +
			"\t\t\tpage, ok := pages[cursor]\n" +
			"\t\t\tif !ok {\n" +
			"\t\t\t\tpage = mockToolsListResult{}\n" +
			"\t\t\t}\n" +
			"\t\t\twriter.Encode(map[string]any{\n" +
			"\t\t\t\t\"jsonrpc\": \"2.0\",\n" +
			"\t\t\t\t\"id\":      req.ID,\n" +
			"\t\t\t\t\"result\":  page,\n" +
			"\t\t\t})\n"
	}

	source += "\t\t}\n\t}\n}\n"

	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatalf("write mock server source: %v", err)
	}

	cmd := exec.Command("go", "build", "-o", tmp+"/mock_server", src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build mock server: %v\n%s", err, out)
	}
	return tmp + "/mock_server"
}
