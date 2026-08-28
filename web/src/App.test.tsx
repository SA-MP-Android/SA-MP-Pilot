// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import './i18n'
import { ServerDialog } from './features/dialogs/server-dialog'
import { Workspace } from './features/workspace/workspace'
import { api } from './api'
import type { Snapshot } from './types'

globalThis.ResizeObserver ??= class {
  disconnect() {}
  observe() {}
  unobserve() {}
} as unknown as typeof ResizeObserver

afterEach(cleanup)

const snapshot: Snapshot = {
  revision: 1,
  syncEpoch: 'test-epoch',
  server: {
    id: 'test-instance',
    host: '127.0.0.1',
    port: 7777,
    nickname: 'WebTest',
    password: '',
    encoding: 'utf-8',
    autoConnect: false,
    emulatePcClientCheck: false,
  },
  connection: {
    status: 'connected',
    serverName: 'Test Server',
    error: '',
    playerCount: 1,
    maxPlayers: 50,
  },
  chat: [],
  players: [],
  nearbyPlayers: [],
  vehicles: [],
  objects: [],
  textDraws: [],
  dialogs: [],
  commands: [],
  activeDialog: {
    id: 12,
    style: 1,
    title: 'Server Login',
    message: '{FF0000}Enter{00FF00} your password',
    button1: 'Login',
    button2: 'Cancel',
    receivedAt: new Date().toISOString(),
  },
  localPlayer: { id: -1, health: 0, armour: 0 },
  vehicleState: { inVehicle: false, passenger: false, vehicleId: -1 },
  keyMask: 0,
  afk: false,
  spawned: true,
  spawnReady: false,
}

describe('ServerDialog', () => {
  it('opens a shadcn dialog for the selected server snapshot', () => {
    render(<ServerDialog value={snapshot} />)
    expect(screen.getByRole('dialog')).toBeTruthy()
    expect(screen.getByText('Server Login')).toBeTruthy()
    expect(screen.getByPlaceholderText('Enter content')).toBeTruthy()
    expect(screen.getByText('Enter').getAttribute('style')).toContain('rgb(255, 0, 0)')
  })

  it('excludes tablist headers and sends the selected row index and first column', async () => {
    const action = vi.spyOn(api, 'action').mockResolvedValue(undefined)
    render(
      <ServerDialog
        value={{
          ...snapshot,
          activeDialog: {
            ...snapshot.activeDialog!,
            id: 20,
            style: 5,
            message: 'Name\tValue\n{FF0000}First\tA\nSecond\tB',
            button1: 'Select',
          },
        }}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /Second/ }))
    fireEvent.click(screen.getByText('Select'))
    await waitFor(() =>
      expect(action).toHaveBeenCalledWith('test-instance', 'dialog', {
        dialogId: 20,
        dialogReceivedAt: snapshot.activeDialog!.receivedAt,
        buttonId: 1,
        listItem: 1,
        inputText: 'Second',
      }),
    )
    action.mockRestore()
  })

  it('defaults an ordinary list to its first row and filters blank rows', async () => {
    const action = vi.spyOn(api, 'action').mockResolvedValue(undefined)
    render(
      <ServerDialog
        value={{
          ...snapshot,
          activeDialog: {
            ...snapshot.activeDialog!,
            id: 21,
            style: 2,
            message: '{FF0000}First \n\nSecond\n',
            button1: 'Select',
          },
        }}
      />,
    )

    fireEvent.click(screen.getByText('Select'))
    await waitFor(() =>
      expect(action).toHaveBeenCalledWith('test-instance', 'dialog', {
        dialogId: 21,
        dialogReceivedAt: snapshot.activeDialog!.receivedAt,
        buttonId: 1,
        listItem: 0,
        inputText: 'First ',
      }),
    )
    action.mockRestore()
  })

  it('uses the filtered ordinary-list index and responds on a double click', async () => {
    const action = vi.spyOn(api, 'action').mockResolvedValue(undefined)
    render(
      <ServerDialog
        value={{
          ...snapshot,
          activeDialog: {
            ...snapshot.activeDialog!,
            id: 22,
            style: 2,
            message: 'First\n\nSecond',
            button1: 'Select',
          },
        }}
      />,
    )

    const second = screen.getByRole('button', { name: 'Second' })
    fireEvent.click(second)
    fireEvent.click(second)
    await waitFor(() =>
      expect(action).toHaveBeenCalledWith('test-instance', 'dialog', {
        dialogId: 22,
        dialogReceivedAt: snapshot.activeDialog!.receivedAt,
        buttonId: 1,
        listItem: 1,
        inputText: 'Second',
      }),
    )
    action.mockRestore()
  })

  it('resets list state when the server reuses a dialog id', async () => {
    const action = vi.spyOn(api, 'action').mockResolvedValue(undefined)
    const firstDialog = {
      ...snapshot.activeDialog!,
      id: 23,
      style: 2,
      message: 'Old first\nOld second',
      button1: 'Select',
      receivedAt: '2026-01-01T00:00:00.000Z',
    }
    const { rerender } = render(<ServerDialog value={{ ...snapshot, activeDialog: firstDialog }} />)
    fireEvent.click(screen.getByRole('button', { name: 'Old second' }))

    rerender(
      <ServerDialog
        value={{
          ...snapshot,
          activeDialog: {
            ...firstDialog,
            message: 'New first\nNew second',
            receivedAt: '2026-01-01T00:00:01.000Z',
          },
        }}
      />,
    )
    fireEvent.click(screen.getByText('Select'))
    await waitFor(() =>
      expect(action).toHaveBeenCalledWith('test-instance', 'dialog', {
        dialogId: 23,
        dialogReceivedAt: '2026-01-01T00:00:01.000Z',
        buttonId: 1,
        listItem: 0,
        inputText: 'New first',
      }),
    )
    action.mockRestore()
  })
})

describe('Workspace quick commands', () => {
  it('shows canonical local and current vehicle health, including zero vehicle health', () => {
    render(
      <Workspace
        value={{
          ...snapshot,
          activeDialog: null,
          localPlayer: { id: 7, health: 73.5, armour: 20 },
          vehicles: [
            {
              id: 42,
              modelId: 411,
              distance: 0,
              health: 900,
              occupied: true,
              driverName: '',
              x: 0,
              y: 0,
              z: 0,
            },
          ],
          vehicleState: { inVehicle: true, passenger: false, vehicleId: 42, health: 0, healthKnown: true },
        }}
        onDelete={vi.fn()}
      />,
    )
    expect(screen.getByText('HP 74 · Armour 20')).toBeTruthy()
    expect(screen.getByText('Vehicle HP 0')).toBeTruthy()
  })

  it('shows the spawn action only after spawn information is ready', () => {
    const { rerender } = render(
      <Workspace
        value={{ ...snapshot, activeDialog: null, spawned: false, spawnReady: false }}
        onDelete={vi.fn()}
      />,
    )
    expect(screen.queryByRole('button', { name: 'Spawn' })).toBeNull()
    rerender(
      <Workspace
        value={{ ...snapshot, activeDialog: null, spawned: false, spawnReady: true }}
        onDelete={vi.fn()}
      />,
    )
    expect(screen.getByRole('button', { name: 'Spawn' })).toBeTruthy()
  })

  it('shows dead instead of awaiting spawn after a death transition', () => {
    render(
      <Workspace
        value={{
          ...snapshot,
          activeDialog: null,
          localPlayer: { ...snapshot.localPlayer, lifeState: 'dead' },
          spawned: false,
          spawnReady: true,
        }}
        onDelete={vi.fn()}
      />,
    )
    expect(screen.getByText('Dead')).toBeTruthy()
    expect(screen.queryByText('Awaiting Spawn')).toBeNull()
  })

  it('requires confirmation before deleting an instance command', async () => {
    const removeCommand = vi.spyOn(api, 'removeCommand').mockResolvedValue(undefined)
    render(
      <Workspace
        value={{
          ...snapshot,
          activeDialog: null,
          commands: [{ id: 'quick-1', serverId: snapshot.server.id, label: 'Example', command: '/example' }],
        }}
        onDelete={vi.fn()}
      />,
    )
    fireEvent.click(screen.getByLabelText('Delete Example'))
    expect(removeCommand).not.toHaveBeenCalled()
    expect(screen.getByText('Delete quick command?')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(removeCommand).toHaveBeenCalledWith(snapshot.server.id, 'quick-1'))
    removeCommand.mockRestore()
  })
})
