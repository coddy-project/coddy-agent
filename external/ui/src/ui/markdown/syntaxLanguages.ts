import { common } from "lowlight";
import ada from "highlight.js/lib/languages/ada";
import armasm from "highlight.js/lib/languages/armasm";
import dart from "highlight.js/lib/languages/dart";
import delphi from "highlight.js/lib/languages/delphi";
import elixir from "highlight.js/lib/languages/elixir";
import fortran from "highlight.js/lib/languages/fortran";
import fsharp from "highlight.js/lib/languages/fsharp";
import glsl from "highlight.js/lib/languages/glsl";
import gml from "highlight.js/lib/languages/gml";
import haskell from "highlight.js/lib/languages/haskell";
import haxe from "highlight.js/lib/languages/haxe";
import lisp from "highlight.js/lib/languages/lisp";
import matlab from "highlight.js/lib/languages/matlab";
import ocaml from "highlight.js/lib/languages/ocaml";
import powershell from "highlight.js/lib/languages/powershell";
import prolog from "highlight.js/lib/languages/prolog";
import scala from "highlight.js/lib/languages/scala";
import scheme from "highlight.js/lib/languages/scheme";
import tcl from "highlight.js/lib/languages/tcl";
import x86asm from "highlight.js/lib/languages/x86asm";
import cobol from "./grammars/cobol.js";
import gdscript from "./grammars/gdscript.js";
import hlsl from "./grammars/hlsl.js";
import tsql from "./grammars/tsql.js";
import vba from "./grammars/vba.js";
import wgsl from "./grammars/wgsl.js";

// Keep the registry explicit: loading every highlight.js language inflates the
// initial UI bundle. Unknown labels remain plaintext; never guess their grammar.
export const syntaxHighlightOptions = {
  detect: false,
  languages: {
    ...common,
    ada,
    armasm,
    dart,
    delphi,
    elixir,
    fortran,
    fsharp,
    glsl,
    gml,
    haskell,
    haxe,
    lisp,
    matlab,
    ocaml,
    powershell,
    prolog,
    scala,
    scheme,
    tcl,
    x86asm,
    cobol,
    gdscript,
    hlsl,
    tsql,
    vba,
    wgsl,
  },
  aliases: {
    javascript: ["react", "react-jsx"],
    typescript: ["react-tsx"],
    css: ["postcss"],
    xml: ["vue", "xslt"],
    x86asm: ["asm", "assembly", "nasm"],
    delphi: ["objectpascal", "object-pascal"],
    gml: ["gamemaker", "game-maker-language"],
    gdscript: ["gd"],
    objectivec: ["objective-c"],
    vbnet: ["visualbasic", "visual-basic"],
    tsql: ["transactsql", "transact-sql", "t-sql"],
    // These dialects intentionally get base-language highlighting only.
    sql: ["plsql", "pl/sql"],
    c: ["opencl", "opencl-c"],
    cpp: ["cuda", "cuda-cpp"],
  },
};
