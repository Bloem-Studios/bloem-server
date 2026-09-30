import ts from "typescript";
import { PRODUCT_NAME } from "./src/lib/product-brand";

/** Keep upstream UI text assertions useful against the branded product.
 * Only test regex literals are adapted; protocol strings and paths stay literal. */
export function brandTestRegexes(code: string): string {
  const source = ts.createSourceFile("test.tsx", code, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const edits: { start: number; end: number; text: string }[] = [];
  function visit(node: ts.Node) {
    if (ts.isRegularExpressionLiteral(node)) {
      const literal = node.getText(source);
      const end = literal.lastIndexOf("/");
      const flags = literal.slice(end + 1);
      const pattern = new RegExp("(^|[\\s(^|])Silo(?=$|[\\s).?!|])", flags.includes("i") ? "gi" : "g");
      const body = literal.slice(1, end).replace(pattern, (_match, before: string) => before + PRODUCT_NAME);
      if (body !== literal.slice(1, end)) edits.push({ start: node.getStart(source), end: node.end, text: "/" + body + "/" + flags });
    }
    ts.forEachChild(node, visit);
  }
  visit(source);
  for (const edit of edits.reverse()) code = code.slice(0, edit.start) + edit.text + code.slice(edit.end);
  return code;
}
