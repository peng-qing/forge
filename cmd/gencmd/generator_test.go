package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildAndGenerateMultipleProtoDirectories(t *testing.T) {
	messages := map[string]messageInfo{
		"C2LLoginRequest": {
			Name:       "C2LLoginRequest",
			ProtoFile:  "c2l/login.proto",
			ImportPath: "forge/api/pb/c2l",
			Package:    "c2l",
		},
		"L2CLoginResponse": {
			Name:       "L2CLoginResponse",
			ProtoFile:  "c2l/login.proto",
			ImportPath: "forge/api/pb/c2l",
			Package:    "c2l",
		},
		"C2SEnterSceneRequest": {
			Name:       "C2SEnterSceneRequest",
			ProtoFile:  "c2s/scene.proto",
			ImportPath: "forge/api/pb/c2s",
			Package:    "c2s",
		},
		"S2CEnterSceneResponse": {
			Name:       "S2CEnterSceneResponse",
			ProtoFile:  "c2s/scene.proto",
			ImportPath: "forge/api/pb/c2s",
			Package:    "c2s",
		},
		"G2LPlayerOfflineNotify": {
			Name:       "G2LPlayerOfflineNotify",
			ProtoFile:  "g2l/session.proto",
			ImportPath: "forge/api/pb/g2l",
			Package:    "g2l",
		},
	}
	commandIDs := map[string]string{
		"C2L_LOGIN":          "COMMAND_ID_C2L_LOGIN",
		"C2S_ENTER_SCENE":    "COMMAND_ID_C2S_ENTER_SCENE",
		"G2L_PLAYER_OFFLINE": "COMMAND_ID_G2L_PLAYER_OFFLINE",
	}

	groups, err := buildCommandGroups(messages, commandIDs, "forge/services/{service}/handler")
	if err != nil {
		t.Fatalf("buildCommandGroups() error = %v", err)
	}
	outputs, count, err := generateOutputs(groups)
	if err != nil {
		t.Fatalf("generateOutputs() error = %v", err)
	}
	if count != 3 {
		t.Fatalf("command count = %d, want 3", count)
	}

	loginSource := string(outputs["c2l/command_gen_login.go"])
	for _, expected := range []string{
		`protocol "forge/api/pb/c2l"`,
		`handler "forge/services/logic/handler"`,
		"type C2LLoginCommand struct{}",
		"req := new(protocol.C2LLoginRequest)",
		"rsp := new(protocol.L2CLoginResponse)",
		"handler.C2LLoginHandler(req, rsp)",
		"router.Register(pb.CommandID_COMMAND_ID_C2L_LOGIN, C2LLoginCommand{})",
	} {
		if !strings.Contains(loginSource, expected) {
			t.Errorf("generated login source does not contain %q", expected)
		}
	}

	for _, name := range []string{
		"c2l/command_gen_register.go",
		"c2s/command_gen_scene.go",
		"c2s/command_gen_register.go",
		"g2l/command_gen_session.go",
		"g2l/command_gen_register.go",
	} {
		if _, exists := outputs[name]; !exists {
			t.Errorf("output %s was not generated", name)
		}
	}
}

func TestBuildRejectsMissingResponse(t *testing.T) {
	messages := map[string]messageInfo{
		"C2LLoginRequest": {
			Name:       "C2LLoginRequest",
			ProtoFile:  "c2l/login.proto",
			ImportPath: "forge/api/pb/c2l",
			Package:    "c2l",
		},
	}
	_, err := buildCommandGroups(messages, map[string]string{
		"C2L_LOGIN": "COMMAND_ID_C2L_LOGIN",
	}, "forge/services/{service}/handler")
	if err == nil || !strings.Contains(err.Error(), "requires response message L2CLoginResponse") {
		t.Fatalf("buildCommandGroups() error = %v, want missing response error", err)
	}
}

func TestBuildRejectsMissingCommandID(t *testing.T) {
	messages := map[string]messageInfo{
		"G2LPlayerOfflineNotify": {
			Name:       "G2LPlayerOfflineNotify",
			ProtoFile:  "g2l/session.proto",
			ImportPath: "forge/api/pb/g2l",
			Package:    "g2l",
		},
	}
	_, err := buildCommandGroups(messages, map[string]string{}, "forge/services/{service}/handler")
	if err == nil || !strings.Contains(err.Error(), "requires COMMAND_ID_G2L_PLAYER_OFFLINE") {
		t.Fatalf("buildCommandGroups() error = %v, want missing command id error", err)
	}
}

func TestBuildSkipsMessagesTargetingClient(t *testing.T) {
	messages := map[string]messageInfo{
		"S2CMonsterBornNotify": {
			Name:       "S2CMonsterBornNotify",
			ProtoFile:  "s2c/monster.proto",
			ImportPath: "forge/api/pb/s2c",
			Package:    "s2c",
		},
		"C2LLoginRequest": {
			Name:       "C2LLoginRequest",
			ProtoFile:  "c2l/login.proto",
			ImportPath: "forge/api/pb/c2l",
			Package:    "c2l",
		},
		"L2CLoginResponse": {
			Name:       "L2CLoginResponse",
			ProtoFile:  "c2l/login.proto",
			ImportPath: "forge/api/pb/c2l",
			Package:    "c2l",
		},
	}
	groups, err := buildCommandGroups(messages, map[string]string{
		"C2L_LOGIN": "COMMAND_ID_C2L_LOGIN",
	}, "forge/services/{service}/handler")
	if err != nil {
		t.Fatalf("buildCommandGroups() error = %v", err)
	}
	if len(groups) != 1 || groups[0].ProtoFile != "c2l/login.proto" {
		t.Fatalf("groups = %#v, want only c2l/login.proto", groups)
	}
}

func TestBuildRejectsDirectionDirectoryMismatch(t *testing.T) {
	messages := map[string]messageInfo{
		"C2LLoginRequest": {
			Name:       "C2LLoginRequest",
			ProtoFile:  "login/login.proto",
			ImportPath: "forge/api/pb/login",
			Package:    "login",
		},
		"L2CLoginResponse": {
			Name:       "L2CLoginResponse",
			ProtoFile:  "login/login.proto",
			ImportPath: "forge/api/pb/login",
			Package:    "login",
		},
	}
	_, err := buildCommandGroups(messages, map[string]string{
		"C2L_LOGIN": "COMMAND_ID_C2L_LOGIN",
	}, "forge/services/{service}/handler")
	if err == nil || !strings.Contains(err.Error(), "must be declared under api/proto/c2l") {
		t.Fatalf("buildCommandGroups() error = %v, want direction directory error", err)
	}
}

func TestScanGeneratedProtobuf(t *testing.T) {
	dir := t.TempDir()
	c2lDir := filepath.Join(dir, "c2l")
	if err := os.MkdirAll(c2lDir, 0o755); err != nil {
		t.Fatalf("create c2l directory: %v", err)
	}
	writeGoFile(t, filepath.Join(c2lDir, "login.pb.go"), `// Code generated by protoc-gen-go. DO NOT EDIT.
// source: c2l/login.proto

package c2l

type C2LLoginRequest struct{}
type L2CLoginResponse struct{}
`)
	writeGoFile(t, filepath.Join(dir, "command.pb.go"), `package pb

type CommandID int32

const (
	CommandID_COMMAND_ID_INVALID CommandID = 0
	CommandID_COMMAND_ID_C2L_LOGIN CommandID = 1
)
`)

	messages, err := scanMessageFiles(dir, "forge/api/pb")
	if err != nil {
		t.Fatalf("scanMessageFiles() error = %v", err)
	}
	if got := messages["C2LLoginRequest"].ImportPath; got != "forge/api/pb/c2l" {
		t.Fatalf("C2LLoginRequest import = %q", got)
	}

	commandIDs, err := scanCommandIDs(dir)
	if err != nil {
		t.Fatalf("scanCommandIDs() error = %v", err)
	}
	if got := commandIDs["C2L_LOGIN"]; got != "COMMAND_ID_C2L_LOGIN" {
		t.Fatalf("C2L_LOGIN enum = %q", got)
	}
}

func TestUpperSnake(t *testing.T) {
	for input, expected := range map[string]string{
		"Login":         "LOGIN",
		"EnterScene":    "ENTER_SCENE",
		"HTTPStatus":    "HTTP_STATUS",
		"CreateRoleV2":  "CREATE_ROLE_V_2",
		"PlayerOffline": "PLAYER_OFFLINE",
	} {
		if got := upperSnake(input); got != expected {
			t.Errorf("upperSnake(%q) = %q, want %q", input, got, expected)
		}
	}
}

func TestExpandHandlerImportPath(t *testing.T) {
	got, err := expandHandlerImportPath("forge/app/{service}/handlers", "logic")
	if err != nil {
		t.Fatalf("expandHandlerImportPath() error = %v", err)
	}
	if got != "forge/app/logic/handlers" {
		t.Fatalf("expandHandlerImportPath() = %q", got)
	}

	_, err = expandHandlerImportPath("forge/services/handler", "logic")
	if err == nil || !strings.Contains(err.Error(), "must contain {service}") {
		t.Fatalf("expandHandlerImportPath() error = %v, want missing placeholder error", err)
	}
}

func TestRemoveStaleGeneratedFiles(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "c2l")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("create nested directory: %v", err)
	}
	stale := filepath.Join(nested, "command_gen_old.go")
	manual := filepath.Join(nested, "command_gen_manual.go")
	writeGoFile(t, stale, "// Code generated by forge-command-gen. DO NOT EDIT.\npackage c2l\n")
	writeGoFile(t, manual, "package c2l\n")

	if err := removeStaleGeneratedFiles(dir, map[string][]byte{}); err != nil {
		t.Fatalf("removeStaleGeneratedFiles() error = %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale generated file still exists: %v", err)
	}
	if _, err := os.Stat(manual); err != nil {
		t.Fatalf("manual file was removed: %v", err)
	}
}

func writeGoFile(t *testing.T, path, source string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
}
