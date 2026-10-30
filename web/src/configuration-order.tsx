import { useCallback, useId, useLayoutEffect, useMemo, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { ConnectError, type Client } from "@connectrpc/connect"
import { ArrowDown, ArrowUp, ListOrdered } from "lucide-react"
import { AssetKind, ConfigurationService } from "./gen/skillhub/configuration/v1/configuration_pb"
import { configurationError, useConfigurationRequest, type AssetMetadata } from "./configuration-state"
import { movedOrder, orderPartitions, partitionKey, requireOrderAcknowledgment, requireOrderSnapshot, type OrderPartition } from "./configuration-order-state"
import { t as translate, formatNumber } from "./i18n"
import { Button } from "./components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogTitle, DialogTrigger } from "./components/ui/dialog"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./components/ui/select"
import "./configuration-order-i18n"

type ConfigurationClient = Client<typeof ConfigurationService>
const kindKeys: Record<number, string> = { [AssetKind.PROVIDER]: "provider", [AssetKind.MCP]: "mcp", [AssetKind.PROMPT]: "prompt", [AssetKind.AGENT_SETTINGS]: "settings", [AssetKind.WORKSPACE]: "workspace" }

function OrderGroup({ owner, client, partition, generation, invalidate, reload }: { owner: string; client: ConfigurationClient; partition: OrderPartition; generation: number; invalidate: () => void; reload: () => void }) {
  const { t } = useTranslation("configurationOrder")
  const queryClient = useQueryClient()
  const key = useMemo(() => ["configuration-order", owner, partition.agentId, partition.kind, generation], [owner, partition.agentId, partition.kind, generation])
  const query = useQuery({
    queryKey: key, retry: false, gcTime: 0, staleTime: Infinity, refetchOnWindowFocus: false,
    queryFn: async ({ signal }) => {
      try { return requireOrderSnapshot((await client.getAssetOrder(partition, { signal })).snapshot, partition) }
      catch (error) { throw new ConnectError("", ConnectError.from(error).code) }
    },
  })
  const request = useConfigurationRequest(invalidate)
  const rows = useRef(new Map<string, HTMLLIElement>())
  const [moved, setMoved] = useState<{ id: string; name: string; position: number; total: number }>()
  useLayoutEffect(() => { if (moved) rows.current.get(moved.id)?.focus() }, [moved])
  const move = async (id: string, direction: -1 | 1) => {
    const previous = query.data
    if (!previous || request.pending || request.blocked || query.isFetching) return
    const ids = movedOrder(previous, id, direction)
    const snapshot = await request.run(async (signal) => requireOrderAcknowledgment((await client.replaceAssetOrder({ ...partition, expectedRevision: previous.revision, assetIds: ids }, { signal })).snapshot, previous, ids), true)
    if (!snapshot) return
    queryClient.setQueryData(key, snapshot)
    const asset = snapshot.assets.find((entry) => entry.id === id)
    if (asset) setMoved({ id, name: asset.name, position: ids.indexOf(id) + 1, total: ids.length })
  }
  return <div className="mt-5 min-w-0 space-y-4" aria-busy={query.isFetching || request.pending}>
    {query.isPending ? <p role="status">{t("loading")}</p> : query.isError ? <p role="alert" className="text-sm text-destructive">{translate(configurationError(ConnectError.from(query.error).code))}</p> : <>
      {query.data.assets.length === 0 ? <p>{t("empty")}</p> : <ol className="space-y-3">{query.data.assets.map((asset, index) => <li key={asset.id} tabIndex={-1} ref={(element) => { if (element) rows.current.set(asset.id, element); else rows.current.delete(asset.id) }} aria-label={asset.name} className="min-w-0 rounded-xl border border-border p-3 outline-offset-4 focus-visible:outline-2 focus-visible:outline-ring">
        <p className="break-words font-medium [overflow-wrap:anywhere]">{asset.name}</p>
        <p className="mt-1 text-sm text-muted-foreground">{t("position", { position: formatNumber(index + 1), total: formatNumber(query.data.assets.length) })}</p>
        <div className="mt-3 flex flex-wrap gap-2"><Button type="button" variant="outline" aria-label={t("up", { name: asset.name })} disabled={index === 0 || request.pending || request.blocked || query.isFetching || query.data.revision === 9223372036854775807n} onClick={() => void move(asset.id, -1)}><ArrowUp size={16} aria-hidden="true" /><span>{t("upLabel")}</span></Button><Button type="button" variant="outline" aria-label={t("down", { name: asset.name })} disabled={index === query.data.assets.length - 1 || request.pending || request.blocked || query.isFetching || query.data.revision === 9223372036854775807n} onClick={() => void move(asset.id, 1)}><ArrowDown size={16} aria-hidden="true" /><span>{t("downLabel")}</span></Button></div>
      </li>)}</ol>}
    </>}
    <div role="status" aria-live="polite" className="min-h-6 text-sm">{request.pending ? t("pending") : moved ? <>{t("saved")} {t("moved", { name: moved.name, position: formatNumber(moved.position), total: formatNumber(moved.total) })}</> : null}</div>
    {request.failure !== undefined && <p role="alert" className="text-sm text-destructive">{translate(configurationError(request.failure))}</p>}
    {request.uncertain && <p role="alert" className="text-sm">{t("uncertain")}</p>}
    {request.blocked && <p className="text-sm text-muted-foreground">{t("blocked")}</p>}
    <Button type="button" variant="outline" disabled={request.pending || query.isFetching} onClick={reload}>{t("reload")}</Button>
    <p className="text-sm text-muted-foreground">{t("readOnly")}</p>
  </div>
}

export function ConfigurationOrderControl({ owner, client, assets, disabled, invalidate }: { owner: string; client: ConfigurationClient; assets: AssetMetadata[]; disabled: boolean; invalidate: () => void }) {
  const { t } = useTranslation("configurationOrder")
  const [open, setOpen] = useState(false)
  const [selected, setSelected] = useState("")
  const [generation, setGeneration] = useState(0)
  const id = useId()
  const partitions = orderPartitions(assets)
  const partition = partitions.find((entry) => partitionKey(entry) === selected)
  const reload = useCallback(() => setGeneration((current) => current + 1), [])
  return <div className="my-4 flex flex-wrap items-center gap-3"><Dialog open={open} onOpenChange={(next) => { setOpen(next); if (!next) { setSelected(""); setGeneration((current) => current + 1) } }}>
    <DialogTrigger asChild><Button type="button" variant="outline" disabled={disabled || partitions.length === 0}><ListOrdered size={16} aria-hidden="true" />{t("title")}</Button></DialogTrigger>
    <DialogContent className="min-w-0 sm:max-w-2xl"><DialogTitle>{t("title")}</DialogTitle><DialogDescription>{t("description")}</DialogDescription><div className="mt-5 space-y-2"><label htmlFor={id}>{t("partition")}</label><Select value={selected} onValueChange={(value) => { setSelected(value); setGeneration((current) => current + 1) }}><SelectTrigger id={id} className="w-full"><SelectValue placeholder={t("choose")} /></SelectTrigger><SelectContent>{partitions.map((entry) => <SelectItem key={partitionKey(entry)} value={partitionKey(entry)}>{entry.agentId} · {t(kindKeys[entry.kind])}</SelectItem>)}</SelectContent></Select></div>
      {partition && <OrderGroup key={`${selected}:${generation}`} owner={owner} client={client} partition={partition} generation={generation} invalidate={invalidate} reload={reload} />}
    </DialogContent>
  </Dialog>{partitions.length === 0 && <p className="text-sm text-muted-foreground">{t("noGroups")}</p>}</div>
}
