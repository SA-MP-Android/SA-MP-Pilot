import { memo, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { EntityGrid } from '@/components/entity-grid'
import { Button } from '@/components/ui/button'
import { ScrollArea } from '@/components/ui/scroll-area'
import { ACTION_ENTER_VEHICLE, ACTION_TELEPORT, NEARBY_REFRESH_MS } from '@/constants'
import { useThrottledValue } from '@/lib/use-throttled-value'
import type { Player, Vehicle, WorldObject } from '@/types'

export interface NearbyTabProps {
  players: Player[]
  vehicles: Vehicle[]
  objects: WorldObject[]
  act: (action: string, data?: unknown) => void
}

export const NearbyTab = memo(function NearbyTab({ players, vehicles, objects, act }: NearbyTabProps) {
  const { t } = useTranslation()
  const nearbyPlayers = useThrottledValue(players, NEARBY_REFRESH_MS)
  const nearbyVehicles = useThrottledValue(vehicles, NEARBY_REFRESH_MS)
  const nearbyObjects = useThrottledValue(objects, NEARBY_REFRESH_MS)

  const content = useMemo(
    () => (
      <>
        <EntityGrid
          empty={t('nearby.playersEmpty')}
          rows={nearbyPlayers.map((entry) => ({
            id: entry.id,
            title: entry.name,
            subtitle: t('nearby.playerDetails', {
              distance: entry.distance.toFixed(1),
              health: entry.health,
              skin: entry.skin,
            }),
            actions: (
              <Button variant="outline" onClick={() => act(ACTION_TELEPORT, entry)}>
                {t('common.teleport')}
              </Button>
            ),
          }))}
        />
        <EntityGrid
          empty={t('nearby.vehiclesEmpty')}
          rows={nearbyVehicles.map((entry) => ({
            id: entry.id,
            title: t('nearby.vehicle', { id: entry.id }),
            subtitle: t('nearby.modelDistance', {
              model: entry.modelId,
              distance: entry.distance.toFixed(1),
            }),
            actions: (
              <>
                <Button variant="outline" onClick={() => act(ACTION_TELEPORT, entry)}>
                  {t('common.teleport')}
                </Button>
                <Button onClick={() => act(ACTION_ENTER_VEHICLE, { vehicleId: entry.id, passenger: false })}>
                  {t('nearby.drive')}
                </Button>
                <Button
                  variant="secondary"
                  onClick={() => act(ACTION_ENTER_VEHICLE, { vehicleId: entry.id, passenger: true })}
                >
                  {t('nearby.passenger')}
                </Button>
              </>
            ),
          }))}
        />
        <EntityGrid
          empty={t('nearby.objectsEmpty')}
          rows={nearbyObjects.map((entry) => ({
            id: entry.id,
            title: t('nearby.object', { id: entry.id }),
            subtitle: t('nearby.modelDistance', {
              model: entry.modelId,
              distance: entry.distance.toFixed(1),
            }),
            actions: (
              <Button variant="outline" onClick={() => act(ACTION_TELEPORT, entry)}>
                {t('common.teleport')}
              </Button>
            ),
          }))}
        />
      </>
    ),
    [nearbyPlayers, nearbyVehicles, nearbyObjects, act, t],
  )

  return (
    <ScrollArea className="h-full pr-3">
      <div className="grid gap-3 md:grid-cols-3">{content}</div>
    </ScrollArea>
  )
})
