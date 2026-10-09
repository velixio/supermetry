import { describe, expect, it } from "vitest";

import {
  buildCodeSamples,
  buildReference,
  preparePrivateRequest,
  type OpenAPIDocument,
} from "./reference";

const document: OpenAPIDocument = {
  openapi: "3.1.0",
  info: { title: "Supermetry API", version: "1.0.0" },
  paths: {
    "/api/public/v1/stats/summary": {
      get: {
        operationId: "statsSummary",
        summary: "Get summary statistics",
        tags: ["Statistics"],
        parameters: [
          {
            name: "site_id",
            in: "query",
            required: true,
            schema: { type: "integer" },
          },
          {
            name: "period",
            in: "query",
            required: true,
            schema: { type: "string", example: "30d" },
          },
          {
            name: "date_from",
            in: "query",
            schema: { type: "string", example: "2026-10-01" },
          },
        ],
        responses: {
          "200": {
            description: "Summary",
            content: {
              "application/json": {
                schema: { $ref: "#/components/schemas/QueryResponse" },
                example: { totals: { visitors: 42 } },
              },
            },
          },
          "401": { $ref: "#/components/responses/Unauthorized" },
        },
      },
    },
  },
  components: {
    responses: {
      Unauthorized: {
        description: "Authentication is required or invalid.",
        content: {
          "application/problem+json": {
            schema: { $ref: "#/components/schemas/Problem" },
          },
        },
      },
    },
    schemas: {
      QueryResponse: {
        type: "object",
        properties: { totals: { type: "object" } },
      },
      Problem: {
        type: "object",
        example: {
          type: "https://supermetry.com/problems/bad-request",
          title: "Bad request",
          status: 400,
        },
      },
    },
  },
};

describe("buildReference", () => {
  it("groups operations by tag and exposes parameters, schemas, and response examples", () => {
    const reference = buildReference(document);

    expect(reference.groups).toHaveLength(1);
    expect(reference.groups[0]?.name).toBe("Statistics");
    expect(reference.groups[0]?.operations[0]).toMatchObject({
      method: "GET",
      path: "/api/public/v1/stats/summary",
      summary: "Get summary statistics",
      schemaNames: ["Problem", "QueryResponse"],
      responseExamples: [
        { status: "200", value: { totals: { visitors: 42 } } },
        {
          status: "401",
          value: {
            type: "https://supermetry.com/problems/unauthorized",
            title: "Unauthorized",
            status: 401,
            detail: "Authentication is required or invalid.",
          },
        },
      ],
    });
    expect(reference.schemas.QueryResponse).toEqual(
      document.components?.schemas?.QueryResponse,
    );
  });

  it("derives a readable example when a response only declares a schema", () => {
    const withoutExample = structuredClone(document);
    const response =
      withoutExample.paths["/api/public/v1/stats/summary"]?.get?.responses?.[
        "200"
      ];
    if (response?.content?.["application/json"])
      delete response.content["application/json"].example;

    expect(
      buildReference(withoutExample).groups[0]?.operations[0]?.responseExamples,
    ).toEqual([
      { status: "200", value: { totals: {} } },
      {
        status: "401",
        value: {
          type: "https://supermetry.com/problems/unauthorized",
          title: "Unauthorized",
          status: 401,
          detail: "Authentication is required or invalid.",
        },
      },
    ]);
  });
});

describe("buildCodeSamples", () => {
  it("builds authenticated curl, JavaScript, Python, and Go requests", () => {
    const operation = buildReference(document).groups[0]!.operations[0]!;
    const samples = buildCodeSamples(
      operation,
      "https://analytics.example.com",
      7,
    );

    expect(samples.curl).toContain("Authorization: Bearer <api-key>");
    expect(samples.curl).toContain("site_id=7&period=30d");
    expect(samples.curl).not.toContain("date_from");
    expect(samples.javascript).toContain(
      "headers: { Authorization: 'Bearer <api-key>' }",
    );
    expect(samples.python).toContain(
      "headers={'Authorization': 'Bearer <api-key>'}",
    );
    expect(samples.go).toContain(
      'req.Header.Set("Authorization", "Bearer <api-key>")',
    );
  });

  it("includes a JSON request example for operations with a body", () => {
    const operation = {
      ...buildReference(document).groups[0]!.operations[0]!,
      method: "POST" as const,
      requestExample: { site_id: 7, metrics: ["visitors"] },
    };
    const samples = buildCodeSamples(
      operation,
      "https://analytics.example.com",
      7,
    );

    expect(samples.curl).toContain(
      '--data \'{"site_id":7,"metrics":["visitors"]}\'',
    );
    expect(samples.javascript).toContain("body: JSON.stringify({");
    expect(samples.python).toContain(
      "json={'site_id': 7, 'metrics': ['visitors']}",
    );
    expect(samples.go).toContain(
      'strings.NewReader(`{"site_id":7,"metrics":["visitors"]}`)',
    );
  });
});

describe("preparePrivateRequest", () => {
  it("rewrites the public route and supplies the selected site and parameter values", () => {
    const operation = buildReference(document).groups[0]!.operations[0]!;

    expect(preparePrivateRequest(operation, 7, { period: "7d" })).toEqual({
      method: "GET",
      path: "/api/private/stats/summary?site_id=7&period=7d",
      body: undefined,
    });
  });

  it("points a query body's site_id at the selected site", () => {
    const operation = {
      ...buildReference(document).groups[0]!.operations[0]!,
      method: "POST" as const,
      path: "/api/public/v1/query",
      parameters: [],
    };

    expect(
      preparePrivateRequest(operation, 7, {}, { site_id: 42, metrics: ["visitors"] }),
    ).toEqual({
      method: "POST",
      path: "/api/private/query",
      body: { site_id: 7, metrics: ["visitors"] },
    });
  });

  it("does not offer private execution for public-only endpoints", () => {
    const operation = {
      ...buildReference(document).groups[0]!.operations[0]!,
      path: "/api/public/v1/events",
    };

    expect(preparePrivateRequest(operation, 7, {})).toBeNull();
  });

  it("does not execute resource mutations from the read-only documentation console", () => {
    const operation = {
      ...buildReference(document).groups[0]!.operations[0]!,
      method: "DELETE" as const,
      path: "/api/public/v1/sites/{site_id}",
    };

    expect(preparePrivateRequest(operation, 7, {})).toBeNull();
  });
});
