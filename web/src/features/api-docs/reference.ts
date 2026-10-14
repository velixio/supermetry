export type JSONSchema = {
  $ref?: string;
  type?: string | string[];
  format?: string;
  description?: string;
  example?: unknown;
  default?: unknown;
  enum?: unknown[];
  properties?: Record<string, JSONSchema>;
  required?: string[];
  items?: JSONSchema;
  additionalProperties?: boolean | JSONSchema;
};

export type OpenAPIParameter = {
  $ref?: string;
  name?: string;
  in?: "path" | "query" | "header" | "cookie";
  required?: boolean;
  description?: string;
  schema?: JSONSchema;
  example?: unknown;
};

type OpenAPIContent = Record<
  string,
  {
    schema?: JSONSchema;
    example?: unknown;
    examples?: Record<string, { value?: unknown }>;
  }
>;

type OpenAPIResponse = {
  $ref?: string;
  description?: string;
  content?: OpenAPIContent;
};

type OpenAPIOperation = {
  operationId?: string;
  summary?: string;
  description?: string;
  tags?: string[];
  security?: unknown[];
  parameters?: OpenAPIParameter[];
  requestBody?: {
    required?: boolean;
    content?: OpenAPIContent;
  };
  responses?: Record<string, OpenAPIResponse>;
};

type OpenAPIPath = Partial<Record<Lowercase<HTTPMethod>, OpenAPIOperation>> & {
  parameters?: OpenAPIParameter[];
};

export type OpenAPIDocument = {
  openapi: string;
  info: { title: string; version: string; description?: string };
  paths: Record<string, OpenAPIPath>;
  components?: {
    schemas?: Record<string, JSONSchema>;
    parameters?: Record<string, OpenAPIParameter>;
    responses?: Record<string, OpenAPIResponse>;
  };
};

export type HTTPMethod = "GET" | "POST" | "PATCH" | "PUT" | "DELETE";

export type ReferenceOperation = {
  id: string;
  method: HTTPMethod;
  path: string;
  summary: string;
  description?: string;
  parameters: Array<
    OpenAPIParameter & { name: string; in: NonNullable<OpenAPIParameter["in"]> }
  >;
  requestSchema?: JSONSchema;
  requestExample?: unknown;
  responseExamples: Array<{ status: string; value: unknown }>;
  schemaNames: string[];
  authenticated: boolean;
};

export type APIReference = {
  groups: Array<{ name: string; operations: ReferenceOperation[] }>;
  schemas: Record<string, JSONSchema>;
};

const methods: Array<Lowercase<HTTPMethod>> = [
  "get",
  "post",
  "patch",
  "put",
  "delete",
];

function referenceName(ref?: string): string | undefined {
  return ref?.startsWith("#/components/schemas/")
    ? ref.split("/").at(-1)
    : undefined;
}

function inferTag(path: string): string {
  if (
    path.startsWith("/health") ||
    path.startsWith("/ready") ||
    path === "/metrics"
  )
    return "Operations";
  const parts = path.split("/").filter(Boolean);
  const resource = parts[3] ?? parts[0] ?? "API";
  const labels: Record<string, string> = {
    stats: "Statistics",
    query: "Statistics",
    realtime: "Statistics",
    heatmaps: "Analytics",
    vitals: "Analytics",
    funnels: "Funnels",
    journeys: "Journeys",
    retention: "Retention",
    sites: "Sites",
    goals: "Goals",
    profiles: "Profiles",
    annotations: "Annotations",
    segments: "Segments",
    events: "Events",
    mcp: "MCP",
    "api-keys": "API keys",
    l: "Short links",
    px: "Tracking pixel",
  };
  return (
    labels[resource] ??
    resource
      .replaceAll("-", " ")
      .replace(/^./, (letter) => letter.toUpperCase())
  );
}

function resolveParameter(
  document: OpenAPIDocument,
  parameter: OpenAPIParameter,
): OpenAPIParameter {
  if (!parameter.$ref) return parameter;
  const name = parameter.$ref.split("/").at(-1);
  return name
    ? (document.components?.parameters?.[name] ?? parameter)
    : parameter;
}

function collectSchemaNames(
  schema: JSONSchema | undefined,
  names: Set<string>,
): void {
  if (!schema) return;
  const name = referenceName(schema.$ref);
  if (name) names.add(name);
  Object.values(schema.properties ?? {}).forEach((property) =>
    collectSchemaNames(property, names),
  );
  collectSchemaNames(schema.items, names);
  if (typeof schema.additionalProperties === "object")
    collectSchemaNames(schema.additionalProperties, names);
}

function contentExample(content: OpenAPIContent | undefined): unknown {
  if (!content) return undefined;
  const media = content["application/json"] ?? Object.values(content)[0];
  if (!media) return undefined;
  if (media.example !== undefined) return media.example;
  const named = media.examples
    ? Object.values(media.examples)[0]?.value
    : undefined;
  return named;
}

function schemaExample(
  document: OpenAPIDocument,
  schema: JSONSchema | undefined,
  seen = new Set<string>(),
): unknown {
  if (!schema) return undefined;
  if (schema.example !== undefined) return schema.example;
  if (schema.default !== undefined) return schema.default;
  if (schema.enum?.length) return schema.enum[0];
  if (schema.$ref) {
    const name = referenceName(schema.$ref);
    if (!name || seen.has(name)) return {};
    seen.add(name);
    return schemaExample(document, document.components?.schemas?.[name], seen);
  }
  const type = Array.isArray(schema.type)
    ? schema.type.find((item) => item !== "null")
    : schema.type;
  if (type === "object" || schema.properties) {
    return Object.fromEntries(
      Object.entries(schema.properties ?? {}).map(([name, property]) => [
        name,
        schemaExample(document, property, new Set(seen)),
      ]),
    );
  }
  if (type === "array") {
    const item = schemaExample(document, schema.items, new Set(seen));
    return item === undefined ? [] : [item];
  }
  if (type === "boolean") return false;
  if (type === "integer" || type === "number") return 0;
  if (type === "string") {
    if (schema.format === "date") return "2026-10-08";
    if (schema.format === "date-time") return "2026-10-08T12:00:00Z";
    return "string";
  }
  return {};
}

function responseExample(
  status: string,
  response: OpenAPIResponse | undefined,
  schema: JSONSchema | undefined,
  value: unknown,
): unknown {
  if (
    referenceName(schema?.$ref) !== "Problem" ||
    !/^\d{3}$/.test(status) ||
    !value ||
    typeof value !== "object" ||
    Array.isArray(value)
  ) {
    return value;
  }
  const code = Number(status);
  const titles: Record<number, string> = {
    400: "Bad request",
    401: "Unauthorized",
    403: "Forbidden",
    404: "Not found",
    429: "Too many requests",
    503: "Service unavailable",
  };
  const title = titles[code] ?? "Request failed";
  return {
    ...(value as Record<string, unknown>),
    type: `https://supermetry.com/problems/${title.toLowerCase().replaceAll(" ", "-")}`,
    title,
    status: code,
    detail: response?.description,
  };
}

export function buildReference(document: OpenAPIDocument): APIReference {
  const grouped = new Map<string, ReferenceOperation[]>();
  for (const [path, item] of Object.entries(document.paths)) {
    for (const method of methods) {
      const operation = item[method];
      if (!operation) continue;
      const parameters = [
        ...(item.parameters ?? []),
        ...(operation.parameters ?? []),
      ]
        .map((parameter) => resolveParameter(document, parameter))
        .filter(
          (
            parameter,
          ): parameter is OpenAPIParameter & {
            name: string;
            in: NonNullable<OpenAPIParameter["in"]>;
          } => Boolean(parameter.name && parameter.in),
        );
      const requestContent = operation.requestBody?.content;
      const requestMedia =
        requestContent?.["application/json"] ??
        (requestContent ? Object.values(requestContent)[0] : undefined);
      const schemaNames = new Set<string>();
      collectSchemaNames(requestMedia?.schema, schemaNames);
      const responseExamples = Object.entries(
        operation.responses ?? {},
      ).flatMap(([status, response]) => {
        const resolved = response.$ref
          ? document.components?.responses?.[
              response.$ref.split("/").at(-1) ?? ""
            ]
          : response;
        const content =
          resolved?.content?.["application/json"] ??
          (resolved?.content ? Object.values(resolved.content)[0] : undefined);
        const example =
          contentExample(resolved?.content) ??
          schemaExample(document, content?.schema);
        const value = responseExample(
          status,
          resolved,
          content?.schema,
          example,
        );
        collectSchemaNames(content?.schema, schemaNames);
        return value === undefined ? [] : [{ status, value }];
      });
      const referenceOperation: ReferenceOperation = {
        id: operation.operationId ?? `${method}-${path}`,
        method: method.toUpperCase() as HTTPMethod,
        path,
        summary: operation.summary ?? operation.operationId ?? path,
        description: operation.description,
        parameters,
        requestSchema: requestMedia?.schema,
        requestExample:
          contentExample(requestContent) ??
          schemaExample(document, requestMedia?.schema),
        responseExamples,
        schemaNames: [...schemaNames].sort(),
        authenticated: operation.security?.length !== 0,
      };
      for (const tag of operation.tags?.length
        ? operation.tags
        : [inferTag(path)]) {
        const operations = grouped.get(tag) ?? [];
        operations.push(referenceOperation);
        grouped.set(tag, operations);
      }
    }
  }
  return {
    groups: [...grouped.entries()].map(([name, operations]) => ({
      name,
      operations,
    })),
    schemas: document.components?.schemas ?? {},
  };
}

function parameterValue(
  parameter: ReferenceOperation["parameters"][number],
  siteId: number,
  values: Record<string, string>,
): string {
  if (parameter.name === "site_id" || parameter.name === "site")
    return String(siteId);
  const supplied = values[parameter.name];
  if (supplied !== undefined) return supplied;
  if (!parameter.required) return "";
  const sample =
    parameter.example ?? parameter.schema?.example ?? parameter.schema?.default;
  return sample === undefined ? "" : String(sample);
}

function operationURL(
  operation: ReferenceOperation,
  origin: string,
  siteId: number,
  values: Record<string, string>,
): string {
  let path = operation.path;
  const query = new URLSearchParams();
  for (const parameter of operation.parameters) {
    const value = parameterValue(parameter, siteId, values);
    if (!value) continue;
    if (parameter.in === "path")
      path = path.replace(`{${parameter.name}}`, encodeURIComponent(value));
    if (parameter.in === "query") query.append(parameter.name, value);
  }
  return `${origin.replace(/\/$/, "")}${path}${query.size ? `?${query}` : ""}`;
}

function pythonLiteral(value: unknown): string {
  if (value === null) return "None";
  if (typeof value === "boolean") return value ? "True" : "False";
  if (typeof value === "number") return String(value);
  if (typeof value === "string")
    return `'${value.replaceAll("\\", "\\\\").replaceAll("'", "\\'")}'`;
  if (Array.isArray(value)) return `[${value.map(pythonLiteral).join(", ")}]`;
  if (typeof value === "object") {
    return `{${Object.entries(value)
      .map(([key, item]) => `${pythonLiteral(key)}: ${pythonLiteral(item)}`)
      .join(", ")}}`;
  }
  return "None";
}

export function buildCodeSamples(
  operation: ReferenceOperation,
  origin: string,
  siteId: number,
): Record<"curl" | "javascript" | "python" | "go", string> {
  const url = operationURL(operation, origin, siteId, {});
  const auth = operation.authenticated
    ? " --header 'Authorization: Bearer <api-key>'"
    : "";
  const compactBody =
    operation.requestExample === undefined
      ? undefined
      : JSON.stringify(operation.requestExample);
  const curlBody = compactBody
    ? ` --header 'Content-Type: application/json' --data '${compactBody}'`
    : "";
  const curl = `curl --request ${operation.method} '${url}'${auth}${curlBody}`;
  const headers = [
    operation.authenticated ? "Authorization: 'Bearer <api-key>'" : "",
    compactBody ? "'Content-Type': 'application/json'" : "",
  ]
    .filter(Boolean)
    .join(", ");
  const javascriptHeaders = headers ? `,\n  headers: { ${headers} }` : "";
  const javascriptBody = compactBody
    ? `,\n  body: JSON.stringify(${JSON.stringify(operation.requestExample, null, 2)})`
    : "";
  const javascript = `const response = await fetch('${url}', {\n  method: '${operation.method}'${javascriptHeaders}${javascriptBody}\n});\nconst data = await response.json();`;
  const pythonHeaders = operation.authenticated
    ? ", headers={'Authorization': 'Bearer <api-key>'}"
    : "";
  const pythonBody = compactBody
    ? `, json=${pythonLiteral(operation.requestExample)}`
    : "";
  const python = `import requests\n\nresponse = requests.${operation.method.toLowerCase()}('${url}'${pythonHeaders}${pythonBody})\nresponse.raise_for_status()\nprint(response.json())`;
  const goAuth = operation.authenticated
    ? '\nreq.Header.Set("Authorization", "Bearer <api-key>")'
    : "";
  const goBody = compactBody ? `strings.NewReader(\`${compactBody}\`)` : "nil";
  const goContentType = compactBody
    ? '\nreq.Header.Set("Content-Type", "application/json")'
    : "";
  const go = `req, err := http.NewRequest("${operation.method}", "${url}", ${goBody})\nif err != nil { log.Fatal(err) }${goAuth}${goContentType}\nresp, err := http.DefaultClient.Do(req)\nif err != nil { log.Fatal(err) }\ndefer resp.Body.Close()`;
  return { curl, javascript, python, go };
}

export function preparePrivateRequest(
  operation: ReferenceOperation,
  siteId: number,
  values: Record<string, string>,
  body?: unknown,
): { method: HTTPMethod; path: string; body: unknown } | null {
  const isReadOperation =
    operation.method === "GET" ||
    (operation.method === "POST" && operation.path.endsWith("/query"));
  if (
    !isReadOperation ||
    !operation.path.startsWith("/api/public/v1/") ||
    operation.path === "/api/public/v1/events" ||
    operation.path === "/api/public/v1/mcp"
  ) {
    return null;
  }
  const privateOperation = {
    ...operation,
    path: operation.path.replace("/api/public/v1", "/api/private"),
  };
  const url = operationURL(privateOperation, "", siteId, values);
  const scopedBody =
    body !== null && typeof body === "object" && !Array.isArray(body) && "site_id" in body
      ? { ...body, site_id: siteId }
      : body;
  return { method: operation.method, path: url, body: scopedBody };
}
