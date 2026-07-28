//go:build llgo && llgo_coro && js && wasm

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package time

import "internal/strconv"

//llgo:skip platformZoneSources initLocal

var platformZoneSources = []string{
	"/usr/share/zoneinfo/",
	"/usr/share/lib/zoneinfo/",
	"/usr/lib/locale/TZ/",
}

// timezoneOffsetMinutes is the one platform fact needed by Go's js/wasm local
// zone initializer. Keeping it as a scalar import avoids loading the complete
// syscall/js reflection bridge merely to construct time.Local.
//
//llgo:coro noblock
//go:wasmimport llgo_js timezone_offset
func timezoneOffsetMinutes() int32

func initLocal() {
	localLoc.name = "Local"

	offset := int(timezoneOffsetMinutes()) * -1
	z := zone{offset: offset * 60, name: "UTC"}
	if offset < 0 {
		z.name += "-"
		offset *= -1
	} else {
		z.name += "+"
	}
	z.name += strconv.Itoa(offset / 60)
	if minutes := offset % 60; minutes != 0 {
		z.name += ":" + strconv.Itoa(minutes)
	}
	localLoc.zone = []zone{z}
}
