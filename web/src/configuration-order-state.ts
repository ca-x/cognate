import { Code, ConnectError } from "@connectrpc/connect"
import { AssetKind, type AssetOrderSnapshot } from "./gen/skillhub/configuration/v1/configuration_pb"
import { metadata, type AssetMetadata } from "./configuration-state"

export type OrderPartition = { agentId: string; kind: AssetKind }
export type OrderSnapshot = OrderPartition & { revision: bigint; assets: AssetMetadata[] }
export const orderKinds = [AssetKind.PROVIDER, AssetKind.MCP, AssetKind.PROMPT, AssetKind.AGENT_SETTINGS, AssetKind.WORKSPACE]

export function partitionKey(partition: OrderPartition) {
  return `${partition.agentId}:${partition.kind}`
}

export function orderPartitions(assets: AssetMetadata[]) {
  const partitions = new Map<string, OrderPartition>()
  for (const asset of assets) {
    if (!asset.agentId || !orderKinds.includes(asset.kind)) continue
    const partition = { agentId: asset.agentId, kind: asset.kind }
    partitions.set(partitionKey(partition), partition)
  }
  return [...partitions.values()]
}

export function requireOrderSnapshot(value: AssetOrderSnapshot | undefined, partition: OrderPartition): OrderSnapshot {
  if (!value || value.agentId !== partition.agentId || value.kind !== partition.kind || value.revision < 0n || value.revision > 9223372036854775807n || value.assets.length > 250) throw new ConnectError("", Code.DataLoss)
  const seen = new Set<string>()
  const assets = value.assets.map((asset) => {
    if (!/^[A-Z2-7]{26}$/.test(asset.id) || seen.has(asset.id) || asset.agentId !== partition.agentId || asset.kind !== partition.kind || asset.revision < 1n || asset.schemaVersion < 1 || asset.schemaVersion > 65535 || asset.createdAtUnix < 0n || asset.updatedAtUnix < 0n) throw new ConnectError("", Code.DataLoss)
    seen.add(asset.id)
    return metadata(asset)
  })
  return { agentId: partition.agentId, kind: partition.kind, revision: value.revision, assets }
}

export function movedOrder(snapshot: OrderSnapshot, id: string, direction: -1 | 1) {
  const ids = snapshot.assets.map((asset) => asset.id)
  const position = ids.indexOf(id)
  const destination = position + direction
  if (position < 0 || destination < 0 || destination >= ids.length || snapshot.revision >= 9223372036854775807n) throw new ConnectError("", Code.FailedPrecondition)
  ;[ids[position], ids[destination]] = [ids[destination], ids[position]]
  return ids
}

export function requireOrderAcknowledgment(value: AssetOrderSnapshot | undefined, previous: OrderSnapshot, ids: string[]) {
  const snapshot = requireOrderSnapshot(value, previous)
  if (snapshot.revision !== previous.revision + 1n || snapshot.assets.length !== ids.length || snapshot.assets.some((asset, index) => asset.id !== ids[index])) throw new ConnectError("", Code.DataLoss)
  return snapshot
}
