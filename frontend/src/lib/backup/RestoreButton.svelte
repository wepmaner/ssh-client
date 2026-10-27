<script lang="ts">
  import { VaultRestorePick, errorText } from '../api'
  import { toast } from '../stores.svelte'
  import Button from '../ui/Button.svelte'
  import RestoreDialog from './RestoreDialog.svelte'
  import { afterRestore } from './restore'

  interface Props {
    variant?: 'primary' | 'outline' | 'ghost'
    disabled?: boolean
  }
  let { variant = 'outline', disabled = false }: Props = $props()

  let path = $state('')

  /** Asks for the file first; a file that is not a backup is refused before the password. */
  async function pick(): Promise<void> {
    try {
      path = await VaultRestorePick()
    } catch (e) {
      toast(errorText(e))
    }
  }
</script>

<Button {variant} {disabled} onclick={pick}>Восстановить из копии</Button>
<RestoreDialog {path} onclose={() => (path = '')} onrestored={afterRestore} />
