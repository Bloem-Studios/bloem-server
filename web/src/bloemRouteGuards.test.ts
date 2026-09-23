import { describe, expect, it } from "vitest";
import ts from "typescript";
import source from "./App.tsx?raw";
import bloemRoutesSource from "./bloem/routes.tsx?raw";

// Contract regression for the actual route tree, not a second test-only router.
// Browser acceptance separately exercises the authenticated destinations.
// Bloem route fragments spliced into App.tsx as `{bloemXRoutes}` are followed
// into src/bloem/routes.tsx so their guards come from the real App tree.
function routeGuards() {
  const parse = (name: string, text: string) =>
    ts.createSourceFile(name, text, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const app = parse("App.tsx", source);
  const bloemRoutes = parse("routes.tsx", bloemRoutesSource);
  const fragments = new Map<string, ts.Expression>();
  bloemRoutes.forEachChild((statement) => {
    if (!ts.isVariableStatement(statement)) return;
    for (const declaration of statement.declarationList.declarations) {
      if (ts.isIdentifier(declaration.name) && declaration.initializer) {
        fragments.set(declaration.name.text, declaration.initializer);
      }
    }
  });
  const routes = new Map<string, string[]>();
  function visit(node: ts.Node, parents: string[], file: ts.SourceFile) {
    let guards = parents;
    if (ts.isJsxExpression(node) && node.expression && ts.isIdentifier(node.expression)) {
      const fragment = fragments.get(node.expression.text);
      if (fragment) {
        visit(fragment, parents, bloemRoutes);
        return;
      }
    }
    const opening = ts.isJsxElement(node)
      ? node.openingElement
      : ts.isJsxSelfClosingElement(node)
        ? node
        : undefined;
    if (opening?.tagName.getText(file) === "Route") {
      const attributes = opening.attributes.properties.filter(ts.isJsxAttribute);
      const element = attributes.find(
        (attribute) => attribute.name.getText(file) === "element",
      )?.initializer;
      const expression = element && ts.isJsxExpression(element) ? element.expression : undefined;
      const guard =
        expression && ts.isJsxSelfClosingElement(expression)
          ? expression.tagName.getText(file)
          : "";
      guards = [
        ...parents,
        ...["PlatformContextGuard", "OrganizationContextGuard"].filter((name) => name === guard),
      ];
      const path = attributes.find(
        (attribute) => attribute.name.getText(file) === "path",
      )?.initializer;
      if (path && ts.isStringLiteral(path)) routes.set(path.text, guards);
    }
    ts.forEachChild(node, (child) => visit(child, guards, file));
  }
  visit(app, [], app);
  return routes;
}

describe("Bloem administrative route ownership", () => {
  it("places engagement authoring exclusively under platform authority", () => {
    expect(routeGuards().get("platform/engagement")).toEqual(["PlatformContextGuard"]);
  });
  it("places audit exclusively under organization authority", () => {
    expect(routeGuards().get("organization/activity")).toEqual(["OrganizationContextGuard"]);
  });
});
