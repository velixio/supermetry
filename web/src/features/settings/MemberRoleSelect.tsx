export type MemberRole = "owner" | "admin" | "editor" | "viewer";

const roleLabels: Record<MemberRole, string> = {
  owner: "Owner",
  admin: "Admin",
  editor: "Editor",
  viewer: "Viewer",
};

export function MemberRoleSelect({
  actorRole,
  value,
  disabled = false,
  allowOwner = true,
  onChange,
}: {
  actorRole: MemberRole;
  value: MemberRole;
  disabled?: boolean;
  allowOwner?: boolean;
  onChange: (role: MemberRole) => void;
}) {
  const roles: MemberRole[] =
    actorRole === "owner" && allowOwner
      ? ["owner", "admin", "editor", "viewer"]
      : value === "owner" && disabled ? ["owner", "admin", "editor", "viewer"] : ["admin", "editor", "viewer"];

  return (
    <select
      aria-label="Member role"
      className="h-9 rounded-lg border bg-background px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50"
      disabled={disabled}
      value={value}
      onChange={(event) => onChange(event.target.value as MemberRole)}
    >
      {roles.map((role) => (
        <option key={role} value={role}>
          {roleLabels[role]}
        </option>
      ))}
    </select>
  );
}
