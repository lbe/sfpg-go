# Third-party notices

sfpg-go is licensed under the [MIT License](LICENSE). Its binaries also embed the
components below, which keep their own licenses.

## ExifTool

- Version: 13.55, embedded through
  [`github.com/lbe/go-exiftool-wasm`](https://github.com/lbe/go-exiftool-wasm) v0.6.0.
- Copyright 2003-2026, Phil Harvey.
- License: free software; you can redistribute it and/or modify it under the same
  terms as Perl itself: either the [Artistic License](licenses/Artistic-1.0-Perl.txt)
  or the [GNU General Public License, version 1](licenses/GPL-1.0.txt) or (at your
  option) any later version.
- Source: <https://exiftool.org/> and <https://github.com/exiftool/exiftool>.

## Perl

- Version: 5.16.3, compiled to WebAssembly by
  [zeroperl](https://github.com/6over3/zeroperl) and embedded through
  `go-exiftool-wasm`.
- Copyright 1987-2012, Larry Wall.
- License: the same terms as ExifTool above, the
  [Artistic License](licenses/Artistic-1.0-Perl.txt) or the
  [GNU General Public License, version 1](licenses/GPL-1.0.txt) or later.
- Source: <https://www.cpan.org/src/>.

## zeroperl

- The Perl 5.16.3 build for WebAssembly above, embedded through `go-exiftool-wasm`.
- Copyright (c) 2025 United States Writing Corportation (spelled so in the
  original).
- License: [MIT](licenses/MIT-zeroperl.txt).
- Source: <https://github.com/6over3/zeroperl>.

## Wuffs

- The Wuffs image decoders, compiled to WebAssembly and embedded through
  [`github.com/lbe/wuffs-wasm`](https://github.com/lbe/wuffs-wasm) v0.6.0.
- Copyright 2023 The Wuffs Authors.
- License: Apache License 2.0 or MIT, at the user's option; sfpg-go uses it
  under the [MIT License](licenses/MIT-Wuffs.txt).
- Source: <https://github.com/google/wuffs>.

## Modifications

The embedded ExifTool and Perl library files have their comments and POD
documentation removed and are stored LZ4-compressed. The Perl interpreter is
compiled to WebAssembly and translated to Go with wasm2go. The build pipeline is
documented in the `go-exiftool-wasm` repository. The Wuffs decoders are compiled
to WebAssembly and translated to Go with wasm2go in `wuffs-wasm`.
