// Vendored from https://github.com/dullin/highlightjs-vba/blob/ec940fd58be5cd9b5612a66e6641c84b7cd0c6e9/src/vba.js
// See vba.LICENSE. Local change: ES module export for GDScript only.
/*
Language: Visual Basic for Applications
Description: Visual Basic for Applications (VBA) is an implementation of Microsoft's event-driven programming language
  Visual Basic 6, which was declared legacy in 2008, and its associated integrated development environment (IDE).
Author: Hugo Leblanc <dullin@hololink.org>
Website: https://docs.microsoft.com/en-us/office/vba/language/reference/keywords-visual-basic-for-applications
  https://bettersolutions.com/vba/syntax/keywords.htm
*/

export default function(hljs) {
  return {
    name: 'Visual Basic for Applications',
    aliases: ['vba'],
    case_insensitive: true,
    keywords: {
      keyword:
        '#if #else #const' +
        'alias and as base binary by byref byval ' + /* a-b */
        'call case compare const custom database declare dim do ' + /* c-d */
        'each else elseif empty end enum erase error event exit explicit for friend function ' + /* e-f */
        'get global goto if implements is ' + /* g-i */
        'let lib like lock loop me mid mod ' + /* j-m */
        'new next not ' + /* n */
        'on option optional or ' + /* o */
        'paramarray preserve private property ptrsafe public ' + /* p */
        'raiseevent redim resume return rset' + /* r */
        'select set static step stop sub ' + /* s */
        'text then to until wend while with withevents', /* t-y */
      built_in:
        'boolean byte cbool cbyte ccur cdate cdbl cint clng clnglng clngptr csng cstr currency cvar ' + /* b-c */
        'date defbool defbbyte defdate defdec defdouble defint deflng deflnglng deflngptr defobj defsng double ' + /* d */
        'iif integer input lbound len long longlong lset object print ' + /* e-p */
        'single seek string time type typeof ubound variant', /* s-u */
      literal:
        'true false nothing null'
    },
    illegal: '//|{|}|endif|gosub|^\\$ ', /* reserved deprecated keywords */
    contains: [
      hljs.inherit(hljs.QUOTE_STRING_MODE, { contains: [{ begin: '""' }] }),
      hljs.COMMENT(
        '\'',
        '$'
      ),
      hljs.C_NUMBER_MODE,
      {
        className: 'meta',
        begin: '#',
        end: '$',
        keywords: { 'meta-keyword': 'if else elseif end region externalsource' }
      }
    ]
  };
}
