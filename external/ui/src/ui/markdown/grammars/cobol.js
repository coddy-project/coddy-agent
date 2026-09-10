// Vendored from https://github.com/otterkit/highlightjs-cobol/blob/f439e861eb8c0deee01b2b61035c272e9320e774/src/cobol.js
// See cobol.LICENSE. Local change: ES module export for GDScript only.
// Copyright 2022 Gabriel Gonçalves <KTSnowy@outlook.com>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

/** @type LanguageFn */
export default function (hljs){

  return {
    name: 'COBOL',
    aliases:['standard-cobol','cobol'],
    case_insensitive: true,
    keywords:{
      $pattern: /[a-zA-Z]+(?:-[a-zA-Z0-9]+)*/,
      // Keywords from the 2022 DIS of the COBOL standard.
      keyword: [
        // A
        'ACCEPT','ACCESS',
        'ACTIVE-CLASS','ADD',
        'ADDRESS','ADVANCING',
        'AFTER','ALIGNED',
        'ALLOCATE','ALPHABET',
        'ALPHABETIC','ALPHABETIC-LOWER',
        'ALPHABETIC-UPPER','ALPHANUMERIC',
        'ALPHANUMERIC-EDITED','ALSO',
        'ALTERNATE','AND',
        'ANY','ANYCASE',
        'ARE','AREA',
        'AREAS','AS',
        'ASCENDING','ASSIGN',
        'AT',
        // B
        'B-AND','B-NOT',
        'B-OR','B-SHIFT',
        'B-SHIFT-LC','B-SHIFT-RC',
        'BY','B-XOR',
        'BASED','BEFORE',
        'BINARY','BINARY-CHAR',
        'BINARY-DOUBLE','BINARY-LONG',
        'BINARY-SHORT','BIT',
        'BLANK','BLOCK',
        'BOOLEAN','BOTTOM',
        // C
        'CALL','CANCEL',
        'CF','CH',
        'CHARACTER','CHARACTERS',
        'CLASS','CLASS-ID',
        'CLOSE','CODE',
        'CODE-SET','COL',
        'COLLATING','COLS',
        'COLUMN','COLUMNS',
        'COMMA','COMMIT',
        'COMMON','COMP',
        'COMPUTATIONAL','COMPUTE',
        'CONFIGURATION','CONSTANT',
        'CONTAINS','CONTENT',
        'CONTINUE','CONTROL',
        'CONTROLS','CONVERTING',
        'COPY','CORR',
        'CORRESPONDING','COUNT',
        'CRT','CURRENCY','CURSOR',
        // D
        'DATA','DATA-POINTER',
        'DATE','DAY',
        'DAY-OF-WEEK','DE',
        'DECIMAL-POINT',
        'DECLARATIVES','DEFAULT',
        'DELETE','DELIMITED',
        'DELIMITER','DEPENDING',
        'DESCENDING','DESTINATION',
        'DETAIL','DISPLAY',
        'DIVIDE','DIVISION',
        'DOWN','DUPLICATES',
        'DYNAMIC',
        // E
        'EC','EDITING',
        'ELSE','EMI',
        'END','END-ACCEPT',
        'END-ADD','END-CALL',
        'END-COMPUTE','END-DELETE',
        'END-DISPLAY','END-DIVIDE',
        'END-EVALUATE','END-IF',
        'END-MULTIPLY','END-OF-PAGE',
        'END-PERFORM','END-RECEIVE',
        'END-READ','END-RETURN',
        'END-REWRITE','END-SEARCH',
        'END-START','END-STRING',
        'END-SUBTRACT','END-UNSTRING',
        'END-WRITE','ENVIRONMENT',
        'EOL','EOP',
        'EQUAL','ERROR',
        'EVALUATE','EXCEPTION',
        'EXCEPTION-OBJECT','EXCLUSIVE-OR',
        'EXIT','EXTEND','EXTERNAL',
        // F
        'FACTORY','FARTHEST-FROM-ZERO',
        'FALSE','FD','FILE',
        'FILE-CONTROL','FILLER',
        'FINAL','FINALLY',
        'FIRST','FLOAT-BINARY-32',
        'FLOAT-BINARY-64','FLOAT-BINARY-128',
        'FLOAT-DECIMAL-16','FLOAT-DECIMAL-34',
        'FLOAT-EXTENDED','FLOAT-INFINITY',
        'FLOAT-LONG','FLOAT-NOT-A-NUMBER',
        'FLOAT-NOT-A-NUMBER-QUIET','FLOAT-NOT-A-NUMBER-SIGNALING',
        'FOOTING','FOR',
        'FORMAT','FREE',
        'FROM','FUNCTION',
        'FUNCTION-ID','FUNCTION-POINTER',
        // G
        'GENERATE','GET',
        'GIVING','GLOBAL',
        'GO','GOBACK',
        'GREATER','GROUP',
        'GROUP-USAGE',
        // H
        'HEADING',
        // I
        'I-O','I-O-CONTROL',
        'IDENTIFICATION','IF',
        'IN','IN-ARITHMETIC-RANGE',
        'INDEX','INDEXED',
        'INDICATE','INHERITS',
        'INITIAL','INITIALIZE',
        'INITIATE','INPUT',
        'INPUT-OUTPUT','INSPECT',
        'INTERFACE','INTERFACE-ID',
        'INTO','INVALID',
        'INVOKE','IS',
        // J&K
        'JUST','JUSTIFIED','KEY',
        // L
        'LAST','LEADING',
        'LEFT','LENGTH',
        'LESS','LIMIT',
        'LIMITS','LINAGE',
        'LINAGE-COUNTER','LINE',
        'LINE-COUNTER','LINES',
        'LINKAGE','LOCAL-STORAGE',
        'LOCALE','LOCATION','LOCK',
        // M
        'MERGE','MESSAGE-TAG',
        'METHOD','METHOD-ID',
        'MINUS','MODE',
        'MOVE','MULTIPLY',
        // N
        'NATIONAL','NATIONAL-EDITED',
        'NATIVE','NEAREST-TO-ZERO',
        'NESTED','NEXT',
        'NO','NOT',
        'NULL','NUMBER',
        'NUMERIC','NUMERIC-EDITED',
        // O
        'OBJECT','OBJECT-COMPUTER',
        'OBJECT-REFERENCE','OCCURS',
        'OF','OFF',
        'OMITTED','ON',
        'OPEN','OPTIONAL',
        'OPTIONS','OR',
        'ORDER','ORGANIZATION',
        'OTHER','OUTPUT',
        'OVERFLOW','OVERRIDE',
        // P
        'PACKED-DECIMAL','PAGE',
        'PAGE-COUNTER','PERFORM',
        'PF','PH',
        'PIC','PICTURE',
        'PLUS','POINTER',
        'POSITIVE','PRESENT',
        'PRINTING','PROCEDURE',
        'PROGRAM','PROGRAM-ID',
        'PROGRAM-POINTER','PROPERTY',
        'PROTOTYPE',
        // R
        'RAISE','RAISING',
        'RANDOM','RD',
        'READ','RECEIVE',
        'RECORD','RECORDS',
        'REDEFINES','REEL',
        'REF','REFERENCE',
        'RELATIVE','RELEASE',
        'REMAINDER','REMOVAL',
        'RENAMES','REPLACE',
        'REPLACING','REPORT',
        'REPORTING','REPORTS',
        'REPOSITORY','RESERVE',
        'RESET','RESUME',
        'RETRY','RETURN',
        'RETURNING','REWIND',
        'REWRITE','RF',
        'RH','RIGHT',
        'ROLLBACK','ROUNDED',
        'RUN',
        // S
        'SAME','SCREEN',
        'SD','SEARCH',
        'SECTION','SELECT',
        'SEND','SELF',
        'SENTENCE','SEPARATE',
        'SEQUENCE','SEQUENTIAL',
        'SET','SHARING',
        'SIGN','SIZE',
        'SORT','SORT-MERGE',
        'SOURCE','SOURCE-COMPUTER',
        'SOURCES','SPECIAL-NAMES',
        'STANDARD','STANDARD-1',
        'STANDARD-2','START',
        'STATUS','STOP',
        'STRING','SUBTRACT',
        'SUM','SUPER',
        'SUPPRESS','SYMBOLIC',
        'SYNC','SYNCHRONIZED',
        'SYSTEM-DEFAULT',
        // T
        'TABLE','TALLYING',
        'TERMINATE','TEST',
        'THAN','THEN',
        'THROUGH','THRU',
        'TIME','TIMES',
        'TO','TOP',
        'TRAILING','TRUE',
        'TYPE','TYPEDEF',
        // U
        'UNIT','UNIVERSAL',
        'UNLOCK','UNSTRING',
        'UNTIL','UP',
        'UPON','USAGE',
        'USE','USE',
        'USER-DEFAULT','USING',
        // V
        'VAL-STATUS','VALID',
        'VALIDATE','VALIDATE-STATUS',
        'VALUE','VALUES',
        'VARYING',
        // W & X
        'WHEN','WITH',
        'WORKING-STORAGE','WRITE',
        'XOR',
      ],
      literal: [
        'ZERO','ZEROES','ZEROS',
        'SPACE','SPACES',
        'HIGH-VALUE','HIGH-VALUES',
        'LOW-VALUE','LOW-VALUES',
        'QUOTE','QUOTES',
        'ALL',
      ]
    },
    contains:
      [
        {
          scope: 'comment',
          begin: /(^[ 0-9a-zA-Z]{1,6}[*])/,
          end: /\n/
        },
        {
          scope: 'comment',
          begin: /(^[ 0-9a-zA-Z]{1,6})/m
        },
        {
          scope: 'doctag',
          begin: />>/,
          end: /\n/
        },
        {
          scope: 'type',
          begin: /(9|S9|V9|X|A)+(\([0-9]*\))+/
        },
        {
          scope: 'operator',
          begin: /(\+| - |\*\*|\*|\/|<>|>=|<=|>|<|=|&|::)/
        },
        {
          scope: 'number',
          begin: /([0-9]+(?:(\.|,)[0-9]+)*)/
        },
        {
          scope: 'string',
          begin: '"', end: '"'
        },
        {
          scope: 'string',
          begin: "'", end: "'"
        },
      ]
  }
}
