import { createFileRoute } from "@tanstack/react-router";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ApiKeysPage } from "@/features/settings/SettingsPages";
import { useOrganization } from "@/features/settings/routeContext";

export const Route = createFileRoute("/$org/settings/api-keys")({
  component: ApiKeysRoute,
});
function ApiKeysRoute() {
  const { org } = Route.useParams();
  const membership = useOrganization(org);
  if (!membership) return null;
  if (membership.role !== "owner" && membership.role !== "admin") {
    return <ApiKeysPage orgId={membership.org.id} role={membership.role} />;
  }
  return (
    <div className="flex flex-col gap-6">
      <ApiKeysPage orgId={membership.org.id} role={membership.role} />
      <McpConnectionGuide />
    </div>
  );
}

const endpoint = `${window.location.origin}/api/public/v1/mcp`;
const config = JSON.stringify(
  {
    mcpServers: {
      panel: {
        type: "http",
        url: endpoint,
        headers: { Authorization: "Bearer <key>" },
      },
    },
  },
  null,
  2,
);

export function McpConnectionGuide() {
  return (
    <section className="mx-auto flex w-full max-w-4xl flex-col gap-4 rounded-xl border bg-card p-5 text-card-foreground">
      <header className="flex flex-col gap-1">
        <h2 className="text-lg font-semibold">Connect an MCP client</h2>
        <p className="text-sm text-muted-foreground">
          Create a key above with stats:read access, then replace &lt;key&gt; in your client configuration.
        </p>
      </header>
      <Tabs defaultValue="claude">
        <TabsList aria-label="MCP client">
          <TabsTrigger value="claude">Claude Desktop</TabsTrigger>
          <TabsTrigger value="cursor">Cursor</TabsTrigger>
          <TabsTrigger value="generic">Generic</TabsTrigger>
        </TabsList>
        {[
          ["claude", "Add this server to Claude Desktop's MCP configuration:"],
          ["cursor", "Add this server to Cursor's MCP configuration:"],
          ["generic", "Use this Streamable HTTP server configuration:"],
        ].map(([value, description]) => (
          <TabsContent className="flex flex-col gap-2" value={value} key={value}>
            <p className="text-sm text-muted-foreground">{description}</p>
            <pre className="overflow-x-auto rounded-lg bg-muted p-3 text-xs"><code>{config}</code></pre>
          </TabsContent>
        ))}
      </Tabs>
    </section>
  );
}
