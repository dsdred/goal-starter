package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/dsdred/goal/cmd/goal/msi"
)

func main() {
	output := flag.String("o", "", "output installer path (required)")
	binary := flag.String("binary", "", "path to goal.exe binary (required)")
	version := flag.String("version", "0.0.0", "version for the installer")
	wixdir := flag.String("wixdir", "", "path to WiX Toolset directory (optional)")
	sfx := flag.Bool("sfx", false, "force SFX installer (skip MSI)")
	flag.Parse()

	// Validate required flags
	if *output == "" {
		fmt.Fprintf(os.Stderr, "Error: -o (output path) is required\n")
		flag.Usage()
		os.Exit(1)
	}

	if *binary == "" {
		fmt.Fprintf(os.Stderr, "Error: -binary (goal.exe path) is required\n")
		flag.Usage()
		os.Exit(1)
	}

	// Show version if requested
	if *version == "--version" || *version == "-version" {
		fmt.Println("GoAl MSI Builder")
		os.Exit(0)
	}

	// Resolve paths
	binaryPath, resolveErr := filepath.Abs(*binary)
	if resolveErr != nil {
		fmt.Fprintf(os.Stderr, "Error resolving binary path: %v\n", resolveErr)
		os.Exit(1)
	}

	outputPath, resolveErr := filepath.Abs(*output)
	if resolveErr != nil {
		fmt.Fprintf(os.Stderr, "Error resolving output path: %v\n", resolveErr)
		os.Exit(1)
	}

	// Determine base directory (project root)
	baseDir, err := findProjectRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not find project root: %v\n", err)
		// Use current directory as fallback
		baseDir = "."
	}

	// Set default paths for optional files
	exampleConfig := filepath.Join(baseDir, "goal.example.json")
	readme := filepath.Join(baseDir, "README.md")
	readmeRu := filepath.Join(baseDir, "README_RU.md")

	// Check if files exist
	for name, path := range map[string]string{
		"example-config": exampleConfig,
		"readme":         readme,
		"readme-ru":      readmeRu,
	} {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Warning: %s not found: %s\n", name, path)
		}
	}

	// Override WiX dir if specified
	if *wixdir != "" {
		msi.SetWiXDir(*wixdir)
	}

	fmt.Println("=== GoAl Installer Builder ===")
	fmt.Printf("  Binary:      %s\n", binaryPath)
	fmt.Printf("  Output:      %s\n", outputPath)
	fmt.Printf("  Version:     %s\n", *version)
	fmt.Printf("  Platform:    %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Printf("  Mode:        %s\n", map[bool]string{true: "SFX", false: "MSF/MSI"}[*sfx])
	fmt.Println()

	// Build the installer
	buildErr := error(nil)
	if *sfx {
		buildErr = msi.BuildSFX(binaryPath, exampleConfig, readme, readmeRu, outputPath, *version)
	} else {
		buildErr = msi.BuildMSI(binaryPath, exampleConfig, readme, readmeRu, outputPath, *version)
	}
	if buildErr != nil {
		fmt.Fprintf(os.Stderr, "Error building installer: %v\n", buildErr)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("[+] Done!")
}

// findProjectRoot attempts to find the project root directory.
func findProjectRoot() (string, error) {
	// Try to find go.mod by walking up the directory tree
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		goModPath := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(goModPath); err == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root
			return "", fmt.Errorf("go.mod not found")
		}
		dir = parent
	}
}
