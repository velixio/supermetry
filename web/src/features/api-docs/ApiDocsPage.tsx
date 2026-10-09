import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";

import { ErrorState } from "@/components/ErrorState";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Separator } from "@/components/ui/separator";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import type { Site } from "@/routes/app.$org";
import {
  buildCodeSamples,
  buildReference,
  preparePrivateRequest,
  type OpenAPIDocument,
  type ReferenceOperation,
} from "./reference";

const openAPIQueryKey = ["openapi", "public"] as const;

async function loadOpenAPI(): Promise<OpenAPIDocument> {
  const response = await fetch("/api/public/openapi.json", {
    credentials: "same-origin",
  });
  if (!response.ok)
    throw new Error(
      `OpenAPI document could not be loaded (${response.status})`,
    );
  return response.json() as Promise<OpenAPIDocument>;
}

function label(value: string): string {
  return value
    .replaceAll("_", " ")
    .replace(/^./, (letter) => letter.toUpperCase());
}

function csrfToken(): string | undefined {
  const prefix = "supermetry_csrf=";
  const cookie = document.cookie
    .split(";")
    .map((part) => part.trim())
    .find((part) => part.startsWith(prefix));
  return cookie ? decodeURIComponent(cookie.slice(prefix.length)) : undefined;
}

function OperationNavigation({
  groups,
  selected,
  onSelect,
}: {
  groups: ReturnType<typeof buildReference>["groups"];
  selected: ReferenceOperation;
  onSelect: (operation: ReferenceOperation) => void;
}) {
  return (
    <nav aria-label="API endpoints" className="flex flex-col gap-5">
      {groups.map((group) => (
        <section className="flex flex-col gap-2" key={group.name}>
          <h2 className="text-sm font-semibold">{group.name}</h2>
          <div className="flex flex-col gap-1">
            {group.operations.map((operation) => (
              <Button
                className="h-auto justify-start px-2 py-1.5 text-left"
                key={`${operation.method}-${operation.path}`}
                aria-current={selected.id === operation.id ? "page" : undefined}
                variant={selected.id === operation.id ? "secondary" : "ghost"}
                onClick={() => onSelect(operation)}
              >
                <code className="text-xs">{operation.method}</code>
                <span className="truncate">{operation.summary}</span>
              </Button>
            ))}
          </div>
        </section>
      ))}
    </nav>
  );
}

function CodeExamples({
  operation,
  siteId,
}: {
  operation: ReferenceOperation;
  siteId: number;
}) {
  const samples = buildCodeSamples(operation, window.location.origin, siteId);
  return (
    <Tabs defaultValue="curl">
      <TabsList aria-label="Request language">
        <TabsTrigger value="curl">curl</TabsTrigger>
        <TabsTrigger value="javascript">JavaScript</TabsTrigger>
        <TabsTrigger value="python">Python</TabsTrigger>
        <TabsTrigger value="go">Go</TabsTrigger>
      </TabsList>
      {Object.entries(samples).map(([language, source]) => (
        <TabsContent key={language} value={language}>
          <pre className="overflow-x-auto rounded-lg bg-muted p-3 text-xs">
            <code>{source}</code>
          </pre>
        </TabsContent>
      ))}
    </Tabs>
  );
}

function TryRequest({
  operation,
  orgId,
  sites,
}: {
  operation: ReferenceOperation;
  orgId: number;
  sites: Site[];
}) {
  const [siteId, setSiteId] = useState(sites[0]?.id ?? 0);
  const [values, setValues] = useState<Record<string, string>>({});
  const [body, setBody] = useState(() =>
    operation.requestExample === undefined
      ? ""
      : JSON.stringify(operation.requestExample, null, 2),
  );
  const [result, setResult] = useState("");
  const [pending, setPending] = useState(false);
  const parameters = operation.parameters.filter(
    (parameter) => parameter.name !== "site_id" && parameter.name !== "site",
  );
  const prepared = preparePrivateRequest(operation, siteId, values);

  async function sendRequest() {
    if (!prepared) return;
    setPending(true);
    try {
      const headers: Record<string, string> = { "X-Supermetry-Org": String(orgId) };
      let requestBody: string | undefined;
      if (body.trim()) {
        const scoped = preparePrivateRequest(
          operation,
          siteId,
          values,
          JSON.parse(body) as unknown,
        );
        requestBody = JSON.stringify(scoped?.body);
        headers["Content-Type"] = "application/json";
      }
      if (prepared.method !== "GET") {
        const csrf = csrfToken();
        if (csrf) headers["X-CSRF-Token"] = csrf;
      }
      const response = await fetch(prepared.path, {
        method: prepared.method,
        credentials: "same-origin",
        headers,
        body: requestBody,
      });
      const text = await response.text();
      let formatted = text;
      try {
        formatted = JSON.stringify(JSON.parse(text) as unknown, null, 2);
      } catch {
        // CSV, empty, and plain-text responses are already readable.
      }
      setResult(
        `${response.status} ${response.statusText}\n${formatted}`.trim(),
      );
    } catch (error) {
      setResult(error instanceof Error ? error.message : "Request failed");
    } finally {
      setPending(false);
    }
  }

  if (!prepared) {
    return (
      <p className="text-sm text-muted-foreground">
        Try it is available for read operations with a private dashboard equivalent.
      </p>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <FieldGroup>
        <Field>
          <FieldLabel htmlFor="api-docs-site">Site</FieldLabel>
          <select
            className="h-9 rounded-md border bg-background px-3 text-sm"
            id="api-docs-site"
            value={siteId}
            onChange={(event) => setSiteId(Number(event.target.value))}
          >
            {sites.map((site) => (
              <option key={site.id} value={site.id}>
                {site.name}
              </option>
            ))}
          </select>
        </Field>
        {parameters.map((parameter) => {
          const inputID = `api-docs-${operation.id}-${parameter.name}`;
          const example = String(
            parameter.example ??
              parameter.schema?.example ??
              parameter.schema?.default ??
              "",
          );
          return (
            <Field key={`${parameter.in}-${parameter.name}`}>
              <FieldLabel htmlFor={inputID}>{label(parameter.name)}</FieldLabel>
              <Input
                id={inputID}
                required={parameter.required}
                placeholder={parameter.required ? undefined : example}
                value={values[parameter.name] ?? (parameter.required ? example : "")}
                onChange={(event) =>
                  setValues((current) => ({
                    ...current,
                    [parameter.name]: event.target.value,
                  }))
                }
              />
              {parameter.description ? (
                <FieldDescription>{parameter.description}</FieldDescription>
              ) : null}
            </Field>
          );
        })}
        {operation.requestSchema ? (
          <Field>
            <FieldLabel htmlFor={`api-docs-${operation.id}-body`}>
              JSON body
            </FieldLabel>
            <Textarea
              className="min-h-32 font-mono text-xs"
              id={`api-docs-${operation.id}-body`}
              value={body}
              onChange={(event) => setBody(event.target.value)}
            />
          </Field>
        ) : null}
      </FieldGroup>
      <Button
        className="self-start"
        disabled={pending || !siteId}
        onClick={() => void sendRequest()}
      >
        {pending ? "Sending…" : "Send request"}
      </Button>
      {result ? (
        <pre
          aria-label="Response"
          className="max-h-96 overflow-auto rounded-lg bg-muted p-3 text-xs"
        >
          <code>{result}</code>
        </pre>
      ) : null}
    </div>
  );
}

function OperationReference({
  operation,
  orgId,
  sites,
  schemas,
}: {
  operation: ReferenceOperation;
  orgId: number;
  sites: Site[];
  schemas: ReturnType<typeof buildReference>["schemas"];
}) {
  return (
    <div className="flex min-w-0 flex-col gap-5">
      <header className="flex flex-col gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <code className="rounded bg-muted px-2 py-1 text-xs font-semibold">
            {operation.method}
          </code>
          <code className="break-all text-sm">{operation.path}</code>
        </div>
        <h2 className="text-xl font-semibold">{operation.summary}</h2>
        {operation.description ? (
          <p className="text-sm text-muted-foreground">
            {operation.description}
          </p>
        ) : null}
      </header>
      <Separator />
      <section className="flex flex-col gap-3">
        <h3 className="font-semibold">Parameters</h3>
        {operation.parameters.length ? (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Location</TableHead>
                <TableHead>Required</TableHead>
                <TableHead>Description</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {operation.parameters.map((parameter) => (
                <TableRow key={`${parameter.in}-${parameter.name}`}>
                  <TableCell className="font-mono text-xs">
                    {parameter.name}
                  </TableCell>
                  <TableCell>{parameter.in}</TableCell>
                  <TableCell>{parameter.required ? "Yes" : "No"}</TableCell>
                  <TableCell>{parameter.description ?? "—"}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        ) : (
          <p className="text-sm text-muted-foreground">No parameters.</p>
        )}
      </section>
      <section className="flex flex-col gap-3">
        <h3 className="font-semibold">Example request</h3>
        <CodeExamples operation={operation} siteId={sites[0]?.id ?? 1} />
      </section>
      <section className="flex flex-col gap-3">
        <h3 className="font-semibold">Example responses</h3>
        {operation.responseExamples.length ? (
          operation.responseExamples.map((response) => (
            <div className="flex flex-col gap-1" key={response.status}>
              <code className="text-xs">HTTP {response.status}</code>
              <pre className="overflow-x-auto rounded-lg bg-muted p-3 text-xs">
                <code>{JSON.stringify(response.value, null, 2)}</code>
              </pre>
            </div>
          ))
        ) : (
          <p className="text-sm text-muted-foreground">
            The response has no example in the specification.
          </p>
        )}
      </section>
      {operation.schemaNames.length ? (
        <section className="flex flex-col gap-3">
          <h3 className="font-semibold">Schemas</h3>
          {operation.schemaNames.map((name) => (
            <details className="rounded-lg border p-3" key={name}>
              <summary className="cursor-pointer font-mono text-sm">
                {name}
              </summary>
              <pre className="mt-3 overflow-x-auto text-xs">
                <code>{JSON.stringify(schemas[name], null, 2)}</code>
              </pre>
            </details>
          ))}
        </section>
      ) : null}
      <section className="flex flex-col gap-3">
        <h3 className="font-semibold">Try it</h3>
        <p className="text-sm text-muted-foreground">
          Uses your signed-in session and the private API. API keys are never
          exposed in the browser.
        </p>
        <TryRequest
          key={operation.id}
          operation={operation}
          orgId={orgId}
          sites={sites}
        />
      </section>
    </div>
  );
}

export function ApiDocsPage({
  orgId,
  sites,
}: {
  orgId: number;
  sites: Site[];
}) {
  const query = useQuery({
    queryKey: openAPIQueryKey,
    queryFn: loadOpenAPI,
    staleTime: Infinity,
  });
  const reference = useMemo(
    () => (query.data ? buildReference(query.data) : undefined),
    [query.data],
  );
  const firstOperation =
    reference?.groups
      .flatMap((group) => group.operations)
      .find(
        (operation) =>
          preparePrivateRequest(operation, sites[0]?.id ?? 0, {}) !== null,
      ) ?? reference?.groups[0]?.operations[0];
  const [selectedID, setSelectedID] = useState<string>();
  const selected =
    reference?.groups
      .flatMap((group) => group.operations)
      .find((operation) => operation.id === selectedID) ?? firstOperation;

  if (query.isPending) {
    return (
      <div className="flex flex-col gap-4">
        <Skeleton className="h-8 w-52" />
        <Skeleton className="h-96 w-full" />
      </div>
    );
  }
  if (query.isError || !reference || !selected) {
    return (
      <ErrorState
        detail={
          query.error instanceof Error
            ? query.error.message
            : "The API reference is empty."
        }
        onRetry={() => void query.refetch()}
      />
    );
  }

  return (
    <div className="flex flex-col gap-5">
      <header className="flex flex-col gap-1">
        <h1 className="text-2xl font-semibold">API reference</h1>
        <p className="text-sm text-muted-foreground">
          Browse the embedded OpenAPI contract, copy typed examples, and test
          read requests against a site.
        </p>
      </header>
      <Card className="min-h-[36rem] lg:grid lg:grid-cols-[16rem_minmax(0,1fr)] lg:gap-0">
        <CardHeader className="border-b lg:border-r lg:border-b-0">
          <CardTitle>Endpoints</CardTitle>
          <CardDescription>Grouped by capability</CardDescription>
          <div className="mt-3">
            <OperationNavigation
              groups={reference.groups}
              selected={selected}
              onSelect={(operation) => setSelectedID(operation.id)}
            />
          </div>
        </CardHeader>
        <CardContent className="py-1 lg:px-6">
          <OperationReference
            operation={selected}
            orgId={orgId}
            sites={sites}
            schemas={reference.schemas}
          />
        </CardContent>
      </Card>
    </div>
  );
}
