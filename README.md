# vcl

A compiler and assembler for PlayStation 2 VU1 microcode. It compiles VCL source (old and new syntax, gasp-style macros) or VSM assembly to a `.vo` object, and includes a disassembler and a VU simulator used by the tests. `ps2build` invokes it for `.vcl` sources.

| Command | Purpose |
|---|---|
| `vcl` | Preprocess, compile and assemble `.vcl` and `.vsm`; cost reports |
| `vcl-objdump` | Inspect and disassemble `.vo` objects (`llvm-objdump`-style options) |

```sh
vcl -c shader.vcl -o shader.vo      # object for linking into an EE program
vcl -S shader.vcl -o shader.vsm     # scheduled assembly text
vcl -fcost-report shader.vcl -S     # static cost of the generated code
vcl-objdump -d shader.vo            # disassemble
```

Options and the cost report are described in [docs/usage.md](docs/usage.md).

## Building

```sh
go build ./cmd/...
```

Requires Go 1.26 or later.

## License

[CC0 1.0](LICENSE).
