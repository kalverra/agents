package review

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kalverra/agents/pkg/git"
)

func TestClassifyFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		file         git.FileDiff
		wantTier     FileTier
		wantCategory string
	}{
		{
			name: "deleted file",
			file: git.FileDiff{
				Path:      "old_service.go",
				Status:    "deleted",
				Deletions: 50,
			},
			wantTier: TierDeletion,
		},
		{
			name: "unreadable file",
			file: git.FileDiff{
				Path:   "tools/docker/.env",
				Status: "unreadable",
			},
			wantTier: TierUnreadable,
		},
		{
			name: "go import reordering only",
			file: git.FileDiff{
				Path:      "pkg/server/server.go",
				Status:    "modified",
				Additions: 4,
				Deletions: 4,
				Patch: `@@ -3,6 +3,6 @@
-import (
-	"strings"
-	"fmt"
-)
+import (
+	"fmt"
+	"strings"
+)
`,
			},
			wantTier:     TierFormatting,
			wantCategory: "imports",
		},
		{
			name: "typescript import changes only",
			file: git.FileDiff{
				Path:      "src/components/button.tsx",
				Status:    "modified",
				Additions: 2,
				Deletions: 2,
				Patch: `@@ -1,3 +1,3 @@
-import { ButtonProps, Button } from "./types";
-import React from "react";
+import React from "react";
+import { Button, ButtonProps } from "./types";
`,
			},
			wantTier:     TierFormatting,
			wantCategory: "imports",
		},
		{
			name: "python import changes only",
			file: git.FileDiff{
				Path:      "api/views.py",
				Status:    "modified",
				Additions: 2,
				Deletions: 1,
				Patch: `@@ -1,2 +1,3 @@
-import sys, os
+import os
+import sys
`,
			},
			wantTier:     TierFormatting,
			wantCategory: "imports",
		},
		{
			name: "whitespace and blank lines only",
			file: git.FileDiff{
				Path:      "config.yaml",
				Status:    "modified",
				Additions: 0,
				Deletions: 3,
				Patch: `@@ -10,5 +10,2 @@
 server:
-  
-
   port: 8080
`,
			},
			wantTier:     TierFormatting,
			wantCategory: "whitespace",
		},
		{
			name: "semantic change with import change",
			file: git.FileDiff{
				Path:      "pkg/server/server.go",
				Status:    "modified",
				Additions: 5,
				Deletions: 3,
				Patch: `@@ -3,2 +3,3 @@
+import "log"
@@ -20,2 +21,3 @@
-func Run() error {
+func Run() error {
+	log.Println("starting")
`,
			},
			wantTier: TierSemantic,
		},
		{
			name: "pure logic change",
			file: git.FileDiff{
				Path:      "router.go",
				Status:    "modified",
				Additions: 10,
				Deletions: 2,
				Patch: `@@ -50,3 +50,5 @@
-	return oldProxy
+	return newProxy
`,
			},
			wantTier: TierSemantic,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := ClassifyFile(tc.file)
			assert.Equal(t, tc.wantTier, res.Tier)
			if tc.wantCategory != "" {
				assert.Equal(t, tc.wantCategory, res.Category)
			}
		})
	}
}
