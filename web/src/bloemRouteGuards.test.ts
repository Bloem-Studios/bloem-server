import { describe, expect, it } from "vitest";
import ts from "typescript";
import source from "./App.tsx?raw";

// Contract regression for the actual route tree, not a second test-only router.
// Browser acceptance separately exercises the authenticated destinations.
function routeGuards() {
  const file = ts.createSourceFile(
    "App.tsx",
    source,
    ts.ScriptTarget.Latest,
    true,
    ts.ScriptKind.TSX,
  );
  const routes = new Map<string, string[]>();
  function visit(node: ts.Node, parents: string[]) {
    let guards = parents;
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
    ts.forEachChild(node, (child) => visit(child, guards));
  }
  visit(file, []);
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
