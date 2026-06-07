<script lang="ts">
  // PhotoField — avatar preview + file picker + remove button. File picker
  // pattern matches mail's insertImage() in Composer.svelte (dynamic
  // <input> creation appended to DOM, click, remove) — bind:this hidden
  // inputs have been observed to fail in some WebKitGTK configurations.
  //
  // Resize happens in Go (extensions/contacts/backend/imaging) so we
  // don't push raw multi-MB image bytes through the Wails bridge any
  // longer than necessary.

  import { _ } from 'svelte-i18n'
  import { Button } from '$lib/components/ui/button'
  import Icon from '@iconify/svelte'
  import Avatar from '$lib/components/kit/Avatar.svelte'
  import { toasts } from '$lib/stores/toast'
  // @ts-ignore - wailsjs bindings
  import { Contacts_ResizeContactPhoto as ResizeContactPhoto } from '$wailsjs/go/app/App'
  import type { PhotoState } from './types'

  interface Props {
    photo: PhotoState
    nameForAvatar: string
    emailForAvatar: string
    disabled?: boolean
  }

  let { photo = $bindable({ data: '', mediaType: '', url: '' }), nameForAvatar = '', emailForAvatar = '', disabled = false }: Props = $props()

  const hasPhotoURLOnly = $derived(!photo.data && !!photo.url)

  function triggerPhotoPicker() {
    const input = document.createElement('input')
    input.type = 'file'
    input.accept = 'image/jpeg,image/png,image/webp,image/gif'
    input.style.display = 'none'
    document.body.appendChild(input)
    input.onchange = async (e) => {
      input.remove()
      const file = (e.target as HTMLInputElement).files?.[0]
      if (file) {
        await handlePhotoFile(file)
      }
    }
    input.click()
  }

  async function handlePhotoFile(file: File) {
    try {
      const rawBase64 = await readFileAsBase64(file)
      const resized = await ResizeContactPhoto(rawBase64)
      if (!resized?.data) {
        toasts.error($_('contacts.toast.photoFailed'))
        return
      }
      photo = { data: resized.data, mediaType: resized.mediaType, url: '' }
    } catch (err) {
      console.error('Photo resize failed:', err)
      toasts.error($_('contacts.toast.photoFailed'))
    }
  }

  function removePhoto() {
    photo = { data: '', mediaType: '', url: '' }
  }

  function readFileAsBase64(file: File): Promise<string> {
    return new Promise((resolve, reject) => {
      const reader = new FileReader()
      reader.onload = () => {
        const result = reader.result as string
        const base64 = result.split(',')[1]
        resolve(base64)
      }
      reader.onerror = () => reject(reader.error)
      reader.readAsDataURL(file)
    })
  }
</script>

<div class="flex items-center gap-4">
  <Avatar
    email={emailForAvatar}
    name={nameForAvatar}
    density="large"
    size={72}
    photoData={photo.data}
    photoMediaType={photo.mediaType}
  />
  <div class="flex flex-col gap-1">
    <div class="flex gap-2">
      <Button variant="outline" size="sm" onclick={triggerPhotoPicker} disabled={disabled}>
        <Icon icon="mdi:image-edit-outline" class="w-4 h-4 mr-1" />
        {$_('contacts.edit.photoChange')}
      </Button>
      {#if photo.data || photo.url}
        <Button variant="ghost" size="sm" onclick={removePhoto} disabled={disabled}>
          {$_('contacts.edit.photoRemove')}
        </Button>
      {/if}
    </div>
    {#if hasPhotoURLOnly}
      <span class="text-xs text-muted-foreground">{$_('contacts.edit.photoUrlOnly')}</span>
    {/if}
  </div>
</div>
