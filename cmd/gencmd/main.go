package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	pbDir := flag.String("pb-dir", "api/pb", "protoc 生成的 .pb.go 文件目录")
	pbImport := flag.String("pb-import", "forge/api/pb", "Protobuf Go 包根导入路径")
	handlerImportPattern := flag.String("handler-import-pattern", "forge/services/{service}/handler", "服务 Handler 包导入路径模板，必须包含 {service}")
	outputDir := flag.String("out-dir", "", "Command 生成代码的根目录（必填）")
	flag.Parse()

	if *outputDir == "" {
		fatal(fmt.Errorf("commandgen: -out-dir is required"))
	}

	messages, err := scanMessageFiles(*pbDir, *pbImport)
	if err != nil {
		fatal(err)
	}

	commandIDs, err := scanCommandIDs(*pbDir)
	if err != nil {
		fatal(err)
	}

	groups, err := buildCommandGroups(messages, commandIDs, *handlerImportPattern)
	if err != nil {
		fatal(err)
	}

	outputs, commandCount, err := generateOutputs(groups)
	if err != nil {
		fatal(err)
	}

	paths, err := writeGeneratedFiles(*outputDir, outputs)
	if err != nil {
		fatal(err)
	}

	for _, path := range paths {
		fmt.Printf("generated %s\n", path)
	}
	fmt.Printf("generated %d files for %d commands\n", len(paths), commandCount)
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
