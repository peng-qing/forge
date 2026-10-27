package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

var (
	protoSourcePattern = regexp.MustCompile(`^//\s*source:\s*(.+\.proto)\s*$`)
	commandIDPattern   = regexp.MustCompile(`^CommandID_(COMMAND_ID_[A-Z0-9_]+)$`)
	messageNamePattern = regexp.MustCompile(`^([CGLS])2([CGLS])([A-Z][A-Za-z0-9]*)(Request|Response|Notify)$`)
)

type commandKind uint8

const (
	commandKindRequest commandKind = iota + 1
	commandKindNotify
)

type messageInfo struct {
	Name       string
	ProtoFile  string
	ImportPath string
	Package    string
}

type commandInfo struct {
	ID       string
	Source   string
	Target   string
	Action   string
	Handler  string
	Command  string
	Request  string
	Response string
	Notify   string
	Kind     commandKind
}

type commandGroup struct {
	ProtoFile         string
	Directory         string
	OutputKey         string
	Register          string
	ImportPath        string
	HandlerImportPath string
	Package           string
	Commands          []commandInfo
}

func (c commandInfo) IsRequest() bool {
	return c.Kind == commandKindRequest
}

func scanMessageFiles(pbDir, pbImportRoot string) (map[string]messageInfo, error) {
	fileSet := token.NewFileSet()
	messages := make(map[string]messageInfo)

	err := filepath.WalkDir(pbDir, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".pb.go") {
			return nil
		}

		file, err := parser.ParseFile(fileSet, filePath, nil, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parse %s: %w", filePath, err)
		}
		protoFile, err := protobufSourcePath(pbDir, filePath, file)
		if err != nil {
			return err
		}

		relativeDir, err := filepath.Rel(pbDir, filepath.Dir(filePath))
		if err != nil {
			return fmt.Errorf("resolve protobuf package for %s: %w", filePath, err)
		}
		importPath := strings.TrimSuffix(pbImportRoot, "/")
		if relativeDir != "." {
			importPath += "/" + filepath.ToSlash(relativeDir)
		}

		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.TYPE {
				continue
			}
			for _, specification := range general.Specs {
				typeSpec, ok := specification.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if _, ok := typeSpec.Type.(*ast.StructType); !ok {
					continue
				}

				name := typeSpec.Name.Name
				if previous, exists := messages[name]; exists {
					return fmt.Errorf("protobuf message %s is declared by %s and %s", name, previous.ProtoFile, protoFile)
				}
				messages[name] = messageInfo{
					Name:       name,
					ProtoFile:  protoFile,
					ImportPath: importPath,
					Package:    file.Name.Name,
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("commandgen: scan protobuf directory %s: %w", pbDir, err)
	}
	if len(messages) == 0 {
		return nil, fmt.Errorf("commandgen: no protobuf messages found in %s; run make proto first", pbDir)
	}
	return messages, nil
}

func scanCommandIDs(pbDir string) (map[string]string, error) {
	fileSet := token.NewFileSet()
	commandIDs := make(map[string]string)

	err := filepath.WalkDir(pbDir, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".pb.go") {
			return nil
		}
		file, err := parser.ParseFile(fileSet, filePath, nil, 0)
		if err != nil {
			return fmt.Errorf("parse %s: %w", filePath, err)
		}
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.CONST {
				continue
			}
			for _, specification := range general.Specs {
				values, ok := specification.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, name := range values.Names {
					matches := commandIDPattern.FindStringSubmatch(name.Name)
					if matches == nil {
						continue
					}
					enumName := matches[1]
					key := strings.TrimPrefix(enumName, "COMMAND_ID_")
					if previous, exists := commandIDs[key]; exists {
						return fmt.Errorf("command id key %s is declared by %s and %s", key, previous, enumName)
					}
					commandIDs[key] = enumName
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("commandgen: scan CommandID in %s: %w", pbDir, err)
	}
	if len(commandIDs) == 0 {
		return nil, fmt.Errorf("commandgen: no COMMAND_ID_* values found in %s; run make proto first", pbDir)
	}
	return commandIDs, nil
}

func buildCommandGroups(messages map[string]messageInfo, commandIDs map[string]string, handlerImportPattern string) ([]commandGroup, error) {
	messageNames := make([]string, 0, len(messages))
	for name := range messages {
		messageNames = append(messageNames, name)
	}
	sort.Strings(messageNames)

	byFile := make(map[string][]commandInfo)
	groupInfo := make(map[string]messageInfo)
	usedResponses := make(map[string]struct{})

	for _, name := range messageNames {
		info := messages[name]
		source, target, action, kind, ok := parseMessageName(name)
		if !ok || kind == "Response" {
			continue
		}
		if source == target {
			return nil, fmt.Errorf("commandgen: message %s has the same source and target", name)
		}
		_, hasService := targetService(target)
		if !hasService {
			// C represents the external client. Server-side Command glue only
			// dispatches messages whose final target is a server process.
			continue
		}
		direction := strings.ToLower(source + "2" + target)
		protoDirectory := strings.Split(info.ProtoFile, "/")[0]
		if protoDirectory != direction {
			return nil, fmt.Errorf("commandgen: message %s must be declared under api/proto/%s", name, direction)
		}

		key := source + "2" + target + "_" + upperSnake(action)
		commandID, exists := commandIDs[key]
		if !exists {
			return nil, fmt.Errorf("commandgen: %s requires COMMAND_ID_%s in command.proto", name, key)
		}
		command := commandInfo{
			ID:      commandID,
			Source:  source,
			Target:  target,
			Action:  action,
			Handler: source + "2" + target + action + "Handler",
			Command: source + "2" + target + action + "Command",
		}

		switch kind {
		case "Request":
			responseName := target + "2" + source + action + "Response"
			response, exists := messages[responseName]
			if !exists {
				return nil, fmt.Errorf("commandgen: %s requires response message %s", name, responseName)
			}
			if response.ProtoFile != info.ProtoFile {
				return nil, fmt.Errorf("commandgen: %s and %s must be declared in the same proto file", name, responseName)
			}
			command.Kind = commandKindRequest
			command.Request = name
			command.Response = responseName
			usedResponses[responseName] = struct{}{}
		case "Notify":
			command.Kind = commandKindNotify
			command.Notify = name
		}

		byFile[info.ProtoFile] = append(byFile[info.ProtoFile], command)
		groupInfo[info.ProtoFile] = info
	}

	for _, name := range messageNames {
		_, _, _, kind, ok := parseMessageName(name)
		if ok && kind == "Response" {
			if _, used := usedResponses[name]; !used {
				return nil, fmt.Errorf("commandgen: response message %s has no matching request", name)
			}
		}
	}

	protoFiles := make([]string, 0, len(byFile))
	for protoFile := range byFile {
		protoFiles = append(protoFiles, protoFile)
	}
	sort.Strings(protoFiles)

	groups := make([]commandGroup, 0, len(protoFiles))
	for _, protoFile := range protoFiles {
		info := groupInfo[protoFile]
		directory := path.Dir(protoFile)
		if directory == "." {
			directory = ""
		}
		outputKey := strings.TrimSuffix(path.Base(protoFile), ".proto")
		commands := byFile[protoFile]
		sort.Slice(commands, func(i, j int) bool { return commands[i].Command < commands[j].Command })
		service := targetServiceName(commands[0].Target)
		handlerImportPath, err := expandHandlerImportPath(handlerImportPattern, service)
		if err != nil {
			return nil, err
		}
		groups = append(groups, commandGroup{
			ProtoFile:         protoFile,
			Directory:         directory,
			OutputKey:         outputKey,
			Register:          "register" + exportName(outputKey) + "Commands",
			ImportPath:        info.ImportPath,
			HandlerImportPath: handlerImportPath,
			Package:           info.Package,
			Commands:          commands,
		})
	}
	if len(groups) == 0 {
		return nil, fmt.Errorf("commandgen: no directional Request or Notify messages found")
	}
	return groups, nil
}

func targetService(target string) (string, bool) {
	switch target {
	case "G":
		return "gateway", true
	case "L":
		return "logic", true
	case "S":
		return "scene", true
	default:
		return "", false
	}
}

func targetServiceName(target string) string {
	service, _ := targetService(target)
	return service
}

func expandHandlerImportPath(pattern, service string) (string, error) {
	const placeholder = "{service}"

	pattern = strings.TrimSpace(filepath.ToSlash(pattern))
	if !strings.Contains(pattern, placeholder) {
		return "", fmt.Errorf("commandgen: handler import pattern %q must contain %s", pattern, placeholder)
	}
	importPath := path.Clean(strings.ReplaceAll(pattern, placeholder, service))
	if importPath == "." || importPath == ".." || strings.HasPrefix(importPath, "../") || path.IsAbs(importPath) {
		return "", fmt.Errorf("commandgen: invalid handler import path %q", importPath)
	}
	return importPath, nil
}

func parseMessageName(name string) (source, target, action, kind string, ok bool) {
	matches := messageNamePattern.FindStringSubmatch(name)
	if matches == nil {
		return "", "", "", "", false
	}
	return matches[1], matches[2], matches[3], matches[4], true
}

func upperSnake(value string) string {
	runes := []rune(value)
	var result strings.Builder
	for i, current := range runes {
		if i > 0 {
			previous := runes[i-1]
			var next rune
			if i+1 < len(runes) {
				next = runes[i+1]
			}
			upperBoundary := unicode.IsUpper(current) && (unicode.IsLower(previous) || unicode.IsDigit(previous) || next != 0 && unicode.IsLower(next))
			digitBoundary := unicode.IsDigit(current) && unicode.IsLetter(previous)
			if upperBoundary || digitBoundary {
				result.WriteByte('_')
			}
		}
		result.WriteRune(unicode.ToUpper(current))
	}
	return result.String()
}

func protobufSourcePath(pbDir, filePath string, file *ast.File) (string, error) {
	for _, comments := range file.Comments {
		for _, comment := range comments.List {
			matches := protoSourcePattern.FindStringSubmatch(comment.Text)
			if matches != nil {
				return normalizeProtoPath(matches[1])
			}
		}
	}
	relative, err := filepath.Rel(pbDir, filePath)
	if err != nil {
		return "", fmt.Errorf("commandgen: resolve protobuf file %s: %w", filePath, err)
	}
	return normalizeProtoPath(strings.TrimSuffix(filepath.ToSlash(relative), ".pb.go") + ".proto")
}

func normalizeProtoPath(value string) (string, error) {
	value = strings.TrimSpace(filepath.ToSlash(value))
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || path.IsAbs(cleaned) {
		return "", fmt.Errorf("commandgen: invalid protobuf source path %q", value)
	}
	return cleaned, nil
}

func generateGroup(group commandGroup) ([]byte, error) {
	var buffer bytes.Buffer
	if err := generatorTemplates.ExecuteTemplate(&buffer, "command", group); err != nil {
		return nil, fmt.Errorf("commandgen: execute command template: %w", err)
	}
	formatted, err := format.Source(buffer.Bytes())
	if err != nil {
		return nil, fmt.Errorf("commandgen: format generated source for %s: %w", group.ProtoFile, err)
	}
	return formatted, nil
}

func generateRegistry(packageName string, groups []commandGroup) ([]byte, error) {
	data := struct {
		Package string
		Groups  []commandGroup
	}{Package: packageName, Groups: groups}

	var buffer bytes.Buffer
	if err := generatorTemplates.ExecuteTemplate(&buffer, "registry", data); err != nil {
		return nil, fmt.Errorf("commandgen: execute registry template: %w", err)
	}
	formatted, err := format.Source(buffer.Bytes())
	if err != nil {
		return nil, fmt.Errorf("commandgen: format generated registry: %w", err)
	}
	return formatted, nil
}

func generateOutputs(groups []commandGroup) (map[string][]byte, int, error) {
	outputs := make(map[string][]byte, len(groups)*2)
	byDirectory := make(map[string][]commandGroup)
	commandCount := 0
	for _, group := range groups {
		source, err := generateGroup(group)
		if err != nil {
			return nil, 0, err
		}
		name := filepath.ToSlash(filepath.Join(group.Directory, "command_gen_"+group.OutputKey+".go"))
		outputs[name] = source
		byDirectory[group.Directory] = append(byDirectory[group.Directory], group)
		commandCount += len(group.Commands)
	}

	for directory, directoryGroups := range byDirectory {
		packageName := directoryGroups[0].Package
		for _, group := range directoryGroups[1:] {
			if group.Package != packageName {
				return nil, 0, fmt.Errorf("commandgen: proto directory %s contains Go packages %s and %s", directory, packageName, group.Package)
			}
		}
		registry, err := generateRegistry(packageName, directoryGroups)
		if err != nil {
			return nil, 0, err
		}
		outputs[filepath.ToSlash(filepath.Join(directory, "command_gen_register.go"))] = registry
	}
	return outputs, commandCount, nil
}

func writeGeneratedFiles(dir string, outputs map[string][]byte) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("commandgen: create output directory %s: %w", dir, err)
	}
	names := make([]string, 0, len(outputs))
	for name := range outputs {
		names = append(names, name)
	}
	sort.Strings(names)

	paths := make([]string, 0, len(names))
	for _, name := range names {
		filePath := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			return nil, fmt.Errorf("commandgen: create output directory for %s: %w", filePath, err)
		}
		if err := os.WriteFile(filePath, outputs[name], 0o644); err != nil {
			return nil, fmt.Errorf("commandgen: write %s: %w", filePath, err)
		}
		paths = append(paths, filePath)
	}
	if err := removeStaleGeneratedFiles(dir, outputs); err != nil {
		return nil, err
	}
	return paths, nil
}

func removeStaleGeneratedFiles(dir string, outputs map[string][]byte) error {
	return filepath.WalkDir(dir, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !isGeneratedFileName(entry.Name()) {
			return nil
		}
		relative, err := filepath.Rel(dir, filePath)
		if err != nil {
			return fmt.Errorf("commandgen: resolve generated file %s: %w", filePath, err)
		}
		if _, exists := outputs[filepath.ToSlash(relative)]; exists {
			return nil
		}
		content, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("commandgen: inspect stale file %s: %w", filePath, err)
		}
		if !bytes.HasPrefix(content, []byte("// Code generated by forge-command-gen. DO NOT EDIT.")) {
			return nil
		}
		if err := os.Remove(filePath); err != nil {
			return fmt.Errorf("commandgen: remove stale file %s: %w", filePath, err)
		}
		return nil
	})
}

func isGeneratedFileName(name string) bool {
	return name == "command_gen.go" || strings.HasPrefix(name, "command_gen_") && strings.HasSuffix(name, ".go")
}

func exportName(value string) string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for i, part := range parts {
		if part == "" {
			continue
		}
		runes := []rune(part)
		runes[0] = unicode.ToUpper(runes[0])
		parts[i] = string(runes)
	}
	return strings.Join(parts, "")
}
