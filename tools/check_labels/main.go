// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package main

import (
	"fmt"
	"gennadium/internal/config"
	"os"
)

func main() {
	mode := "sections"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}

	fields, sections := config.ReflectConfigMeta()

	switch mode {
	case "fields":
		for _, f := range fields {
			fmt.Println(f.Key)
		}
	case "sections":
		for _, s := range sections {
			fmt.Printf("section:%s (depth=%d)\n", s.ID, s.Depth)
		}
	case "all":
		for _, s := range sections {
			fmt.Printf("section:%s\n", s.ID)
		}
		for _, f := range fields {
			fmt.Printf("field:%s\n", f.Key)
		}
	}
}
