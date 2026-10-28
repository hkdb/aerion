# Sync Rework

### Details of the sync rework

- Release connection before the per-folder STATUS pass
- NOOP health probe is skipped for connections used within the last 10 seconds
- Folder sync skips STATUS for folders the same pass is about to sync and pipelines the remaining STATUS commands
- Fixed sidebar unread count collapsing to the local window count after marking, moving or deleting messages
- Dead IMAP connections are dropped from the pool on release
- Sync error badge clears after a successful account sync
- IMAP pool no longer opens connections in bursts: More normalized and reliable sync-all
