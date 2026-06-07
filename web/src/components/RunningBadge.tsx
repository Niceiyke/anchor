import { useQuery } from "@tanstack/react-query";
import { api } from "../api";

interface C { name: string; state: string }

// RunningBadge shows whether an app's container is actually up, derived from the
// server's live container list. It matches the app's container name exactly
// (Dockerfile apps -> "<name>") or by compose prefix ("<name>-<service>-N").
// The underlying containers query is shared (deduped by react-query) across all
// apps on the same server.
export function RunningBadge({
  serverId,
  containerName,
  online,
}: {
  serverId: string;
  containerName?: string;
  online: boolean;
}) {
  const { data: containers = [], isLoading, isError } = useQuery({
    queryKey: ["containers", serverId],
    queryFn: () => api.get<C[]>(`/api/servers/${serverId}/containers`),
    enabled: !!serverId && online && !!containerName,
    refetchInterval: 8000,
    retry: false,
    staleTime: 5000,
  });

  if (!online) return <span className="badge failed" title="agent offline">offline</span>;
  if (!containerName) return null;
  if (isError) return <span className="badge" title="couldn't reach agent">unknown</span>;
  if (isLoading) return <span className="badge">…</span>;

  const matches = containers.filter(
    (c) => c.name === containerName || c.name.startsWith(containerName + "-"),
  );
  if (matches.length === 0) return <span className="badge" title="no container found">not running</span>;
  const running = matches.some((c) => c.state === "running");
  return (
    <span className={"badge " + (running ? "success" : "failed")}>
      <span className={"dot " + (running ? "on" : "off")} />
      {running ? "running" : "stopped"}
    </span>
  );
}
