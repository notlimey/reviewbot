package main

var extensions = map[string]string{
	".ts":    "typescript",
	".tsx":   "typescript",
	".cs":    "csharp",
	".razor": "csharp",
	".go":    "go",
	".py":    "python",
	".js":    "javascript",
	".jsx":   "javascript",
}

var skipDirs = map[string]bool{
	"node_modules": true, "bin": true, "obj": true,
	".git": true, ".next": true, "dist": true,
	"vendor": true, "build": true, ".nuxt": true,
	"coverage": true, "__pycache__": true,
}
