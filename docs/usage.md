# Using vcl

`vcl [options] <file.vcl|file.vsm>`. The input language comes from the extension (`-x vcl|vsm` overrides it). A `.vsm` file is assembled directly and skips the compiler.

## Source conventions

- Instructions are indented. A word in column 0 is a label.
- A program starts with `--enter` / `--endenter`, ends with `--exit` / `--endexit`, and the file ends with `.END`. Without `.END` the compiler prints "END missing from end of file." and continues.
- `.syntax new` selects bracketed fields (`vertex[x]`, `mul acc, a, b[w]`). `-n` does the same from the command line.
- Unknown identifiers become virtual registers. Vector or integer is inferred from use.
- `--LoopCS a,b` on the line of a loop label (or the line after it) marks the loop as a software pipelining candidate. The loop must use plain `lq`/`sq` addressing, not `lqi`/`sqi`, and no `xgkick` inside.
- `.name Foo` emits global labels `Foo_CodeStart` and `Foo_CodeEnd` around the code.
- Macros use `.macro name args` / `.endm` with `\arg` substitution.

## Phases

| Option | Result |
|---|---|
| `-E` | Preprocess and print |
| `-fsyntax-only` | Parse and allocate registers, emit nothing |
| `-S` | Scheduled VSM text |
| `-c` | `.vo` object (default) |
| `-o <file>` | Output file, `-` for standard output |

The assembler selects VU mode itself when the text has no `.vu` line.

## Preprocessing

`-I <dir>`, `-D <name[=value]>`, `-U <name>`, `-include <file>`, `-fpreprocessed`, `-MD`, `-MMD`, `-MF <file>`, `-MT <target>`. `-MD` writes a make-style dependency file next to the output, which is what `ps2build` reads.

## Code generation

| Option | Effect |
|---|---|
| `-O0`, `-O1`, `-O2` | No pipelining or search; no pipelining; full (default) |
| `-fno-software-pipeline`, `-fno-loops` | Do not software pipeline loops |
| `-finterlock` | Drop padding for VF and VI hazards and rely on the hardware interlocks. Flag hazards stay padded. On a console this was not faster than the padded output, so it is off by default |
| `-fno-search` | Keep the first register assignment |
| `-fverbose-vsm` | Copy source lines into the VSM text as comments |
| `-fshow-encoding` | With `-S`, add the instruction bytes as comments |

`-fno-auto-end-bit`, `-fno-align`, `-fgenerate-mpg` and `-funique-prefix=` are accepted and ignored.

## Reports

| Option | Effect |
|---|---|
| `-Rregalloc` | Print the register allocation |
| `-Rschedule` | Print, per loop, whether it was software pipelined and why not |
| `--print-schedule-slots[=json]` | Print the issue slots per block |
| `--print-instructions[=json]` | Print the instruction table |

## Cost reports

`-fcost-report[=<file>]` prints the static cost of the generated code: cycles per block, issue and wait stalls, slot use, and totals weighted by loop depth (100 per nesting level). The model counts stalls from `div`/`sqrt` throughput and from `waitq`/`waitp`; it does not model VF or VI interlocks, so treat it as a lower bound.

| Option | Effect |
|---|---|
| `-fcost-report-format=text\|json\|markdown` | Output format |
| `-fcost-loop-weights=ps2gl\|<label>=<n>,...` | Weight named loops |
| `-fcost-baseline=<file.vsm>` | Compare against another output |
| `-Wcost-regression` | Warn when slower than the baseline; with `-Werror` it fails |
| `-fcost-compare-list=<manifest>` | Compare `baseline candidate` file pairs, no input file |
| `-fcost-compare-list-check=<metric>` | Fail when a candidate is slower for the metric |

## vcl-objdump

`-d`, `-h`, `-t`, `-r`, `-s`, `-j <section>`, `--start-address`, `--stop-address`, `--no-show-raw-insn`, `--no-addresses`. With `--no-addresses --no-show-raw-insn`, `-d` prints text that assembles back to the same object.

## ps2build

A `.vcl` source on an `ee` target is compiled with `vcl -c -MD -MF`, using the target's include directories and defines, and the object is linked into the program. Put `vcl` in the ps2build tools directory.
