import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'

import { appMessages } from '@/app/messages'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent } from '@/components/ui/dialog'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { toast } from '@/components/ui/toast'
import { Tooltip } from '@/components/ui/tooltip'
import { accountMessages } from '@/features/accounts/messages'
import { accountsKey } from '@/features/accounts/queries'
import type { Account } from '@/features/accounts/types'
import { validProxy } from '@/features/settings/schema'
import { api } from '@/lib/api'
import { fill, useMessages } from '@/lib/i18n'

// ProxyDialog 是账户代理的编辑对话框，关闭后焦点回到 trigger
export function ProxyDialog({
  account,
  trigger,
  onClose,
}: {
  account: Account
  trigger: HTMLElement | null
  onClose: () => void
}) {
  const m = useMessages(accountMessages)
  const app = useMessages(appMessages)
  const client = useQueryClient()
  const [value, setValue] = useState(account.proxy ?? '')
  const proxy = value.trim()
  const valid = validProxy(proxy)
  const changed = proxy !== (account.proxy ?? '')
  const save = useMutation({
    mutationFn: () =>
      api<Account>('/api/accounts/' + encodeURIComponent(account.id), {
        method: 'PATCH',
        json: { proxy },
      }),
    onSuccess: (result) => {
      client.setQueryData<{ accounts: Account[] }>(
        accountsKey,
        (data) =>
          data && {
            accounts: data.accounts.map((item) => (item.id === result.id ? result : item)),
          },
      )
      toast({ tone: 'success', title: fill(m.proxySaved, { email: account.id }) })
      onClose()
    },
    onError: (error) => {
      toast({
        tone: 'error',
        title: fill(m.proxyFailed, { email: account.id, message: error.message }),
      })
    },
  })
  const blocked = !changed ? m.proxyUnchanged : !valid ? m.proxyInvalid : undefined
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose()
      }}
    >
      <DialogContent
        title={fill(m.proxyTitle, { email: account.id })}
        description={m.proxyDescription}
        closeLabel={app.closeDialog}
        returnFocus={trigger}
        footer={
          <>
            <Button onClick={onClose}>{m.cancel}</Button>
            <Tooltip disabledReason={blocked}>
              <Button
                type="submit"
                form="account-proxy-form"
                variant="primary"
                disabled={blocked !== undefined}
                loading={save.isPending}
              >
                {m.proxySave}
              </Button>
            </Tooltip>
          </>
        }
      >
        <form
          id="account-proxy-form"
          noValidate
          onSubmit={(event) => {
            event.preventDefault()
            if (blocked === undefined) save.mutate()
          }}
        >
          <Field
            id="account-proxy"
            label={m.proxyLabel}
            description={m.proxyHint}
            error={valid ? undefined : m.proxyInvalid}
          >
            <Input
              id="account-proxy"
              value={value}
              onChange={(event) => {
                setValue(event.target.value)
              }}
              placeholder="socks5://127.0.0.1:1080"
              spellCheck={false}
              autoComplete="off"
              aria-invalid={valid ? undefined : true}
              aria-describedby={valid ? 'account-proxy-description' : 'account-proxy-error'}
              className="font-mono"
            />
          </Field>
        </form>
      </DialogContent>
    </Dialog>
  )
}
