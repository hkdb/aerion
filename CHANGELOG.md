# CHANGELOG

**v0.2.2 - 05-09-2026**
---

- Made contact circles themeable
- Added live theme change preview
- Added Adwaita themes (Light/Dark)
- Added Breeze themes (Light/Dark)
- Added Catppuccin themes (Latte/Frappe/Macchiato/Mocha)
- Added Dracula theme
- Added Github themes (Light/Soft Dark/Dark)
- Added Tokyo Night theme
- Added Nord themes (Light/Dark)
- Added Pop! themes (Light/Dark)
- Added VS Code themes (Light/Dark)
- Added Yaru themes (Light/Dark)


**v0.2.1 - 05-07-2026**
---

- Added thread and message focus mode - [#129](https://github.com/hkdb/aerion/issues/129)
- Added contact circle enable/disable
- Added French translation
- Fixed keyboard expand/collapse of focused messages
- Fixed untranslated drop down menu items
- Fixed iCloud Empty Trash - [#136](https://github.com/hkdb/aerion/issues/136)
- Added ESLint for frontend linting


**v0.2.0 - 04-29-2026**
---

- Refactored N+1 query on folder switch - [#117](https://github.com/hkdb/aerion/issues/117)
- Fixed IMAP server side search regression
- Fixed settings tab icons - [#125](https://github.com/hkdb/aerion/issues/125)
- Fixed plain text links - [#113](https://github.com/hkdb/aerion/issues/113)


**v0.1.39 - 04-27-2026**
---

As of 2026-04-26, Aerion is CASA Tier 2 certified and verified by Google so oAuth2 sign-ins will no longer be blocked.

v0.1.39 is a major milestone that includes some remaining originally planned basic features and a substantial amount of bug fixes/refinements focused on making existing features and functions much more reliable/stable. It will serve as a solid foundation for us to continue the further development of this mail client.

- Added image block logic to composer to avoid leaks
- Added folder subscription for auto sync - [#83](https://github.com/hkdb/aerion/issues/83)
- Added optional accent bar for unread messages - [#92](https://github.com/hkdb/aerion/issues/92)
- Added copy text in viewer context menu - [#77](https://github.com/hkdb/aerion/issues/77)
- Added select all in viewer context menu - [#77](https://github.com/hkdb/aerion/issues/77)
- Enable native context menu in composer - [#77](https://github.com/hkdb/aerion/issues/77)
- Added Shared Mailbox support for Microsoft 365 - [#93](https://github.com/hkdb/aerion/issues/93)
- Improved copy/move to folder selection dialog
- Improved invalid encoding handling
- Fixed another ghost message issue
- Fixed invalid e-mail date hang
- Fixed identity switch on replies and forwards
- Fixed draft save and send race condition
- Added proper smtp connect timeout
- Added proper IMAP STARTTLS connect timeout
- Fixed inefficient serilization for inline images and attachments
- Added inline image and attachment size limit
- Fixed inline images on replies and forwards
- Fixed reply/forward signature placement
- Added AttachConsole to for Windows builds for debug output
- Fixed murena.io CardDav - [#86](https://github.com/hkdb/aerion/issues/86)
- Fixed some HK translations
- Fixed non-UTF filename attachment open and download
- Fixed save to sent folder behavior - [#98](https://github.com/hkdb/aerion/issues/98)
- Improved composer formating
- Fixed unchecked rand.Read() in Cryptographic Code
- Backfilled missing DB error handling
- Added proper panic recovery
- Fixed pasted inline image sending
- Fixed undo delete regression
- Fixed name and subject preview decoding - [#104](https://github.com/hkdb/aerion/issues/104)
- Fixed provider icons consistency - [#102](https://github.com/hkdb/aerion/issues/102)
- Added better pgp and s/mime error feedback
- Added a wider range of PGP keys and S/MIME certs support - [#107](https://github.com/hkdb/aerion/issues/107)

**v0.1.38 - 03-22-2026**
---

- Fixed message list refresh on IDLE sync
- Fixed orphaned deleted messages in message list
- Fixed orphaned sync error messages
- Increased go test coverage
- Bumped to Node 24 (LTS)
- GA: skip manifest commit if test build


**v0.1.37 - 03-18-2026**
---

- Changed copy to and move to folder selection to dialog box instead
- Improved moved message handling
- Fixed copy and delete logic for Gmail
- Fixed threading copies of message across folders together
- Fixed post bulk delete focus - [#81](https://github.com/hkdb/aerion/issues/81)
- Fixed post send conversation refresh
- Fixed default from identity for replies


**v0.1.36 - 03-17-2026**
---

- Fixed username auto-fill in add account dialog
- Fixed attachment warning logic - [#79](https://github.com/hkdb/aerion/issues/79)
- Added u-inbox reload guards for better post sync behavior
- Added additional display render error detection - [#74](https://github.com/hkdb/aerion/issues/74)
- Fixes for [#78](https://github.com/hkdb/aerion/issues/78) and [#76](https://github.com/hkdb/aerion/issues/76):
    - Eliminated duplicate event emission from IDLE body fetch 
    - Eliminated redundant webkit calls
    - Cache image allowlist on frontend
    - Handle messages still downloading better
    - Fixed Timer Leak in scheduleMarkAsRead
    - Increased max concurrent db connections
    - Added stale guards
    - Reload necessary messages only during sync
    - Filter unified inbox reloads to inbox folders only

**Note:** Bumped GA and Flatpak node version to 22


**v0.1.35 - 03-13-2026**
---

- Fixed spinning wheel of death in Image Tab of Settings - [#73](https://github.com/hkdb/aerion/issues/73)
- Bumping Github Actions version


**v0.1.34 - 03-13-2026**
---

- Added Images tab to Settings Dialog to manage Always Load lists
- Security - Remove OAuth debug
- Security - Fix attachment file perms
- Security - Sanitize attachment filename
- Security - Validate paths in OpenFile and OpenFolder
- Security - Fix IPC socket TOCTOU with umask
- Security - Strip CRLF in writeHeader
- Cleanup - Removed dead UnblockRemoteImages function
- Cleanup - image loading logic
- Added CardDAV returning time.RFC1123Z (purelymail) workaround - [#71](https://github.com/hkdb/aerion/issues/71)
- Added CardDAV returning unquoted Etag (mailbox.org) workaround - [#26](https://github.com/hkdb/aerion/issues/26)
- Fixed message list checkboxes not responding to shift click - [#67](https://github.com/hkdb/aerion/issues/67)

**Note:** This release is compiled with a new Client ID from the Microsoft newly verified account. However, as per [#29](https://github.com/hkdb/aerion/issues/29), it still doesn't completely solve the oauth "Admin Approval" problem unless your Microsoft 365 administrator has intentionally switched to approve Microsoft verified apps (not the default) in the org settings.


**v0.1.33 - 03-11-2026**
---

- Added dynamic title to detached composer
- Fixed frontend warning
- Updated npm packages
- Improved flags values guarding
- Fixed show attachment in folder - [#69](https://github.com/hkdb/aerion/issues/69)
- Fixed downloading synthetically named attachments
- Added proper flatpak attachments opening from toast logic
- Improved has_attachment marking


**v0.1.32 - 03-10-2026**
---

- Fix - Use reply-to on replies - [#64](https://github.com/hkdb/aerion/issues/64)
- Fixed shift + click selection regression - [#67](https://github.com/hkdb/aerion/issues/67)
- Fixed detached composer title bar - [#65](https://github.com/hkdb/aerion/issues/65)
- Fixed start hidden busy cursor - [#66](https://github.com/hkdb/aerion/issues/66)


**v0.1.31 - 03-05-2026**
---

- Fixed title bar setting regression - [#57](https://github.com/hkdb/aerion/issues/57)


**v0.1.30 - 03-05-2026**
---

- Extracted theme logic from App.svelte into a dedicated Svelte store
- Added a Dark (Balanced) theme
- Added a Light (Balanced) theme
- Added tables and HTML mode to signature composer
- Added option to use native title bar/decorations - [#53](https://github.com/hkdb/aerion/issues/53)
- Added display of reply-to, cc, and bcc if not empty - [#54](https://github.com/hkdb/aerion/issues/54)
- Added always load image setting - [#40](https://github.com/hkdb/aerion/issues/40)
- Fixed Cosmic Desktop bug - needs testing - [#55](https://github.com/hkdb/aerion/issues/55)
- Added workaround instructions for GPU driver bugs - [#56](https://github.com/hkdb/aerion/issues/56)


**v0.1.29 - 02-26-2026**
---

- Toast message to provide feedback for successful link clicks
- Cross accounts from field
- Handle external mailto calls
- Added composer tab in settings 
- Allow setting detached composer as default
- Choose default or detached composer to handle mailto links
- Allow setting plaintext as default
- Moved read receipt setting to composer tab
- Cross account from field
- Fixed drag and drop inline images and attachments - [#41](https://github.com/hkdb/aerion/issues/41)
- Fixed star buttons and states - [#42](https://github.com/hkdb/aerion/issues/42)
- Fixed links in threads [#48](https://github.com/hkdb/aerion/issues/48)
- Fixed attachment logic and extraction for non-text parts - needs a force resync to apply
- Fixed orphaned drafts - [#47](https://github.com/hkdb/aerion/issues/47)
- Fixed flatpak attachment download - [#51](https://github.com/hkdb/aerion/issues/51)
- Consolidated duplicate code between composer and detached composer
- Close conversation viewer if deleted
- Don't auto-open next message if in vertical mobile layout - [#30](https://github.com/hkdb/aerion/issues/30)
- Fixed empty from field - [#39](https://github.com/hkdb/aerion/issues/39)


**v0.1.28 - 02-24-2026**
---

- Slight visual adjustments to the message list checkboxes
- Always show checkboxes on message list when in vertical mobile layout - [#30](https://github.com/hkdb/aerion/issues/30)
- Added per folder filters for unread, starred, and attachments - [#37](https://github.com/hkdb/aerion/issues/37)
- Refactored MessageList.svelte for better maintainability and performance
- Resuming Flathub submission

**Note** to **Flathub** users: A massive amount of features and fixes were in v0.1.25 - v0.1.27 which were not released to Flathub. Check the [Release Page](https://github.com/hkdb/aerion/releases) to see these changes.


**v0.1.27 - 02-23-2026**
---

- Made IMAP folders with sub-folders collapsible
- Identity aware PGP and S/MIME
- Improved guard rails for PGP and S/MIME import, sign, encrypt, and decrypt
- Added multi-language support for missing dynamic message translations
- Proper flatpak implementation of autostart on login - [#33](https://github.com/hkdb/aerion/issues/33)
- Fixed nested IMAP folders fetching - [#34](https://github.com/hkdb/aerion/issues/34)
- Fixed empty or encrypted body preview in message list


**v0.1.26 - 02-22-2026**
---

- Fixed delete silently failing on proton and other generic providers - [#31](https://github.com/hkdb/aerion/issues/31)


**v0.1.25 - 02-21-2026**
---

- Added run in background - [#15](https://github.com/hkdb/aerion/issues/15)
- Added launch hidden - [#15](https://github.com/hkdb/aerion/issues/15)
- Added launch on startup - [#15](https://github.com/hkdb/aerion/issues/15)
- Added Wake and net detection for Windows and Mac
- Added Clickable notifications for Windows and Mac
- Added Empty Trash button for Trash folders - [#21](https://github.com/hkdb/aerion/issues/21)
- Added multi-language support foundation - [#10](https://github.com/hkdb/aerion/issues/10)
- Added 中文(香港), 中文(台灣), 中文(中国)
- Added IMAP search - [#24](https://github.com/hkdb/aerion/issues/24)
- Added Responsive layout to handle both tiling and mobile - [#8](https://github.com/hkdb/aerion/issues/8)
- Fixed sync race condition when moving message during post move sync
- Fixed Trash folder detection to include Bin
- Cleaned up and reorganized sync engine code to be more maintainable

**Note:** Not submitting this release to Flathub until [this issue](https://github.com/flathub/io.github.hkdb.Aerion/issues/6) is resolved.


**v0.1.24 - 02-18-2026**
---

- GMail app password fix - [#22](https://github.com/hkdb/aerion/issues/22)
- Fixed dialog boxes blurry fonts - [#23](https://github.com/hkdb/aerion/issues/23)
- Added context menu to folder pane - [#21](https://github.com/hkdb/aerion/issues/21)
- Close conversation viewer when a message is marked as unread
- Added right alt for triggering context menu with keyboard 


**v0.1.23 - 02-16-2026**
---

- Fixed race condition on marking message read when notification clicked


**v0.1.22 - 02-16-2026**
---

- Fixed wake from sleep flow - [#17](https://github.com/hkdb/aerion/issues/17)
- Added proper network state monitoring
- Improved wake, scheduled syncs, idle, and status logic with net state
- Added proper logic for offline mode
- Fixed S/MIME algo - [#13](https://github.com/hkdb/aerion/issues/13)


**v0.1.21 - 02-14-2026**
---

- Added PGP support - needs more testing
- Added S/MIME support - needs more testing
- Fixed composer rapid enter lag issue with 0 margin `<p>` instead of `<br>`
- Added auto refresh of draft folder on discard
- Added logic to prevent uneccessary reloads of loaded conversations if there's no change
- Fixed draft synced to server indication regression
- Fixed inserted images and attachments saved in draft folder
- Max window size fix [#4](https://github.com/hkdb/aerion/issues/4)
- Auto-focus to the To: field on launch of new composer and on forwards
- Fixed reliability issues with attach file and insert image
- Fixed deletion while syncing
- Improved dead connections handling which makes wake from sleep more reliable & should fix [#9](https://github.com/hkdb/aerion/issues/9)
- Fixed delete mail from trash [#9](https://github.com/hkdb/aerion/issues/9)
- Added reply, reply-all, and forward of a specific message
- Fixed move mail from trash back to inbox
- Improved Sent Folder detection (Wrong sent folder mapping will break threading)
- Ctrl+A when focused on message list will select all messages [#14](https://github.com/hkdb/aerion/issues/14)
- Ctrl+A when focused on conversation viewer will select all text of the expanded email in viewport
    

**v0.1.20 - 02-11-2026**
---

- Added resolution change detection - [#4](https://github.com/hkdb/aerion/issues/4)
- Added trusted self-signed cert flow and store - [#6](https://github.com/hkdb/aerion/issues/6)
- Improved imap login logic
- Improved image blocking to include CSS loaded images
- Enabled horizontal scroll in conversation viewer
    

**v0.1.19 - 02-09-2026**
---

- Fixed terms acceptance visibility
- Enhanced system theme detection
- Fixed idle.go/server.go
- Implemented a workaround for calling dialog through portal
- Removed redundant desktop-file-edit commands from Flatpak manifest
    

**v0.1.18 - 02-08-2026**
---

- Converted to Flathub build from source


**v0.1.17 - 02-07-2026**
---

- Added refresh conversation viewer if new mail arrives in the thread
- Added auto scroll to the bottom (newest mail) in conversation viewer on long threads
- GA/Flathub submission fix


**v0.1.16 - 02-07-2026**
---

- Removed flatpak perm that's already allowed by default
- Fixed hash calculation for Flatpak build and Flathub submission


**v0.1.15 - 02-05-2026**
---

- Refactored Linux notifications to use org.freedesktop.portal.Desktop
- Kept DBUS direct notifications if launched with --dbus-notify
- Added trigger to refocus to Aerion if notification is clicked
- Added `install.sh` and `uninstall.sh` to Linux binary release
- Distribute binary tarballs with assets instead of just binary for Linux
- Fixed flatpak app ID
- Flathub submission fixes
- New Github Actions worksflow that makes much more sense


**v0.1.14 - 02-05-2026**
---

- Finalized flatpak submission


**v0.1.13 - 02-04-2026**
---

- Fixed links that don't open in browser (ie. Linkedin, etc)
- Added show link on hover
- Added context menu for links so users can choose to copy the link instead of clicking it directly


**v0.1.12 - 02-03-2026**
---

- Removed AppImage build
- Implemented Flatpak build


**v0.1.11 - 02-02-2026**
---

- Fixed detached composer theme
- Fixed message focus on refresh
- Improved transitions for smoother UX


**v0.1.10 - 02-02-2026**
---

- Added other themes:
    - Dark (Gray)
    - Light (Blue)
    - Light (Orange)


**v0.1.9 - 01-29-2026**
---

- Ability to disable window title bar in settings
- Added an AppImage just for Immutable/Atomic distros [#1](https://github.com/hkdb/aerion/issues/1)


**v0.1.8 - 01-29-2026**
---

- Fixed AppImage support for more popular immutable/atomic distros


**v0.1.7 - 01-29-2026**
---

- Fixed AppImage regression for non-atomic distros
- Sticking with 22.04 LTS to build since 20.04 doesn't have webkit2gtk-4.1 and 20.04 is only a few months away from EOS.


**v0.1.6 - 01-28-2026**
---

- Fixed signature insertion on reply
- Fixed replies not being tracked in conversations
- Fixed ghost recipient on reply-All 
- Cleaned up console.log/warn in frontend
- Added ability to delete single message from conversation
- Sync draft folder after saving draft from inline composer
- Reload conversation viewer after saving draft
- Added keyboard driven single message delete (focus on conversation viewer pane --> tab to msg --> delete)


**v0.1.5 - 01-27-2026**
---

- Bundle icons instead of downloading on launch
- Improved AppImage compatibility


**v0.1.4 - 01-26-2026**
---

- Fixed delete flow regression
- Fixed null reference errors


**v0.1.3 - 01-25-2026**
---

- Added "Mark as NOT Spam" to spam folders
- Improved Google contact sync error handling
- Auto-focus on the first message of search results on enter
- Added cancel folder sync
- Added shortcut keys for sync all accounts and folder sync


**v0.1.2 - 01-22-2026**
---

- Looses keyboard control if e-mail content was clicked
- Autofocus on first message when switched to new folder
- Disable focus on conversation viewer when links are clicked


**v0.1.1 - 01-19-2026**
---

- Compile AppImage with Ubuntu 22.04 instead to improve compatibility with older systems


**v0.1.0 - 01-16-2026**
---

- First release - ALPHA
