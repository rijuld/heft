package weigh

import (
	"os"
	"path/filepath"
	"strings"
)

// License guesses the SPDX identifier of the license in dir. It returns ""
// when there is no license file, and "unknown" when the text isn't one of
// the common licenses below. It's a guess for a human to confirm, not a
// legal opinion: inlining code means keeping its notice either way.
func License(dir string) (spdx, file string) {
	if dir == "" {
		return "", ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", ""
	}
	for _, e := range entries {
		name := strings.ToLower(e.Name())
		if e.IsDir() || !(strings.HasPrefix(name, "license") || strings.HasPrefix(name, "licence") || strings.HasPrefix(name, "copying")) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		return classify(string(b)), filepath.Join(dir, e.Name())
	}
	return "", ""
}

func classify(text string) string {
	t := strings.Join(strings.Fields(strings.ToLower(text)), " ")
	has := func(s ...string) bool {
		for _, x := range s {
			if !strings.Contains(t, x) {
				return false
			}
		}
		return true
	}
	switch {
	case has("apache license", "version 2.0"):
		return "Apache-2.0"
	case has("mozilla public license", "2.0"):
		return "MPL-2.0"
	case has("gnu affero general public license"):
		return "AGPL-3.0"
	case has("gnu lesser general public license", "version 3"):
		return "LGPL-3.0"
	case has("gnu lesser general public license"), has("gnu library general public license"):
		return "LGPL-2.1"
	case has("gnu general public license", "version 3"):
		return "GPL-3.0"
	case has("gnu general public license"):
		return "GPL-2.0"
	case has("permission is hereby granted, free of charge"):
		return "MIT"
	case has("redistribution and use in source and binary forms", "neither the name"),
		has("redistribution and use in source and binary forms", "endorse or promote"):
		return "BSD-3-Clause"
	case has("redistribution and use in source and binary forms"):
		return "BSD-2-Clause"
	case has("permission to use, copy, modify, and/or distribute"), has("permission to use, copy, modify, and distribute this software for any purpose"):
		return "ISC"
	case has("this is free and unencumbered software"):
		return "Unlicense"
	case has("boost software license"):
		return "BSL-1.0"
	case has("provided 'as-is', without any express or implied warranty"):
		return "Zlib"
	}
	return "unknown"
}

// permissive reports whether copying a few functions out of code under
// this license only obliges you to keep its notice.
func permissive(spdx string) bool {
	switch spdx {
	case "MIT", "BSD-2-Clause", "BSD-3-Clause", "ISC", "Apache-2.0", "Unlicense", "BSL-1.0", "Zlib":
		return true
	}
	return false
}
