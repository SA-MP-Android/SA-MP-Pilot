import { useState } from 'react'
import { RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'

export function SettingsDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [refreshing, setRefreshing] = useState(false)

  async function refreshGPCI() {
    setRefreshing(true)
    try {
      await api.refreshGpci()
      setConfirmOpen(false)
      toast.success(t('settings.gpciRefreshed'))
    } catch (error) {
      toast.error((error as Error).message)
    } finally {
      setRefreshing(false)
    }
  }

  return (
    <>
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('settings.title')}</DialogTitle>
            <DialogDescription>{t('settings.description')}</DialogDescription>
          </DialogHeader>
          <div className="flex items-center justify-between gap-4 rounded-lg border p-4">
            <div className="min-w-0">
              <p className="font-medium">{t('settings.gpci')}</p>
              <p className="text-muted-foreground text-sm">{t('settings.gpciDescription')}</p>
            </div>
            <Button variant="outline" className="shrink-0" onClick={() => setConfirmOpen(true)}>
              <RefreshCw />
              {t('settings.refreshGpci')}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
      <AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('settings.refreshTitle')}</AlertDialogTitle>
            <AlertDialogDescription>{t('settings.refreshMessage')}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={refreshing}>{t('common.cancel')}</AlertDialogCancel>
            <AlertDialogAction
              disabled={refreshing}
              onClick={(event) => {
                event.preventDefault()
                void refreshGPCI()
              }}
            >
              {t('settings.refreshGpci')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}
