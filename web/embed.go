// Copyright (c) 2026 Ruokun (Tommy) Niu
// Licensed under the MIT License. See LICENSE file in the project root for details.

package web

import "embed"

//go:embed static/*
var StaticFiles embed.FS
