<script lang="ts">
  import Icon from '@iconify/svelte'
  import { Button } from '$lib/components/ui/button'
  import { accountStore } from '$lib/stores/accounts.svelte'
  import AccountDialog from './AccountDialog.svelte'
  // @ts-ignore - wailsjs path
  import type { account } from '../../../../wailsjs/go/models'
  import { _ } from '$lib/i18n'

  // Filter out shared mailboxes — they're managed from the parent account's Identity tab
  const regularAccounts = $derived(accountStore.accounts.filter(acc => !acc.account.sharedMailboxParentId))

  // Dialog state
  let showAccountDialog = $state(false)
  let editingAccount = $state<account.Account | null>(null)

  function openEdit(acc: account.Account) {
    editingAccount = acc
    showAccountDialog = true
  }

  function openAdd() {
    editingAccount = null
    showAccountDialog = true
  }

  function handleDialogClose() {
    showAccountDialog = false
    editingAccount = null
  }

  async function moveUp(index: number) {
    if (index <= 0) return
    const ids = accountStore.accounts.map(a => a.account.id)
    // Swap with previous
    ;[ids[index - 1], ids[index]] = [ids[index], ids[index - 1]]
    await accountStore.reorderAccounts(ids)
  }

  async function moveDown(index: number) {
    if (index >= accountStore.accounts.length - 1) return
    const ids = accountStore.accounts.map(a => a.account.id)
    // Swap with next
    ;[ids[index], ids[index + 1]] = [ids[index + 1], ids[index]]
    await accountStore.reorderAccounts(ids)
  }
</script>

<div class="space-y-4">
  <h3 class="text-sm font-medium flex items-center gap-2">
    <Icon icon="mdi:email-multiple" class="w-4 h-4" />
    {$_('settingsAccounts.emailAccounts')}
  </h3>

  {#if accountStore.loading}
    <div class="flex items-center justify-center py-4">
      <Icon icon="mdi:loading" class="w-5 h-5 animate-spin text-muted-foreground" />
    </div>
  {:else if regularAccounts.length === 0}
    <div class="text-sm text-muted-foreground py-4 text-center">
      <p class="mb-3">{$_('settingsAccounts.noAccountsConfigured')}</p>
      <Button size="sm" onclick={openAdd}>
        <Icon icon="mdi:plus" class="w-4 h-4 mr-1" />
        {$_('settingsAccounts.addAccount')}
      </Button>
    </div>
  {:else}
    <div class="space-y-2">
      {#each regularAccounts as accWithFolders, index (accWithFolders.account.id)}
        {@const acc = accWithFolders.account}
        <div class="p-3 border border-border rounded-lg flex items-center gap-3">
          <!-- Order number -->
          <div class="w-6 h-6 rounded-full bg-muted flex items-center justify-center text-xs font-medium text-muted-foreground">
            {index + 1}
          </div>

          <!-- Account color dot -->
          <div
            class="w-3 h-3 rounded-full shrink-0"
            style:background-color={acc.color || '#6b7280'}
          ></div>

          <!-- Account info -->
          <div class="flex-1 min-w-0">
            <div class="font-medium text-sm truncate">{acc.name}</div>
            <div class="text-xs text-muted-foreground truncate">{acc.email}</div>
          </div>

          <!-- Up/Down buttons -->
          <div class="flex items-center gap-1">
            <Button
              size="icon"
              variant="ghost"
              class="h-7 w-7"
              onclick={() => moveUp(index)}
              disabled={index === 0}
              title={$_('settingsAccounts.moveUp')}
            >
              <Icon icon="mdi:chevron-up" class="w-4 h-4" />
            </Button>
            <Button
              size="icon"
              variant="ghost"
              class="h-7 w-7"
              onclick={() => moveDown(index)}
              disabled={index === accountStore.accounts.length - 1}
              title={$_('settingsAccounts.moveDown')}
            >
              <Icon icon="mdi:chevron-down" class="w-4 h-4" />
            </Button>
          </div>

          <!-- Edit button -->
          <Button
            size="icon"
            variant="ghost"
            class="h-7 w-7"
            onclick={() => openEdit(acc)}
            title={$_('settingsAccounts.editAccount')}
          >
            <Icon icon="mdi:pencil" class="w-4 h-4" />
          </Button>
        </div>
      {/each}

      <!-- Add button -->
      <Button size="sm" variant="outline" class="w-full" onclick={openAdd}>
        <Icon icon="mdi:plus" class="w-4 h-4 mr-1" />
        {$_('settingsAccounts.addAccount')}
      </Button>
    </div>
  {/if}
</div>

<!-- Account Dialog -->
<AccountDialog
  bind:open={showAccountDialog}
  editAccount={editingAccount}
  onClose={handleDialogClose}
/>
