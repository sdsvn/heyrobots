---
co-review: design
---
# Check for updates and update from the app

## Context
Today the only way to update is to run the install script again in a terminal. Nothing tells you a new release exists, and the server doesn't even know its own version: the app's settings showed a hand-typed "0.3" until yesterday. On a Mac the app and the server are two halves of one install. They come from the same release and should be updated together, in one tap.

The server does the updating, because it already runs on the computer, keeps running when the app closes, and is what the web app and the phones talk to. The work goes in a new `internal/update` package and two new endpoints. The server learns its version from the release build, the same way the app now does.

## Design
- The server knows its version and checks for a newer one
  - the release build stamps the tag into `heyrobotsd`; a build from source says "dev"
  - once a day, and when the app asks, it reads the latest release from GitHub's public API
  - `GET /update` returns the running version, the latest one, whether it's newer, the release notes, and whether this install can update itself
  - a "dev" build, or one whose binary the server can't overwrite, such as one installed by a package manager, says why it can't update instead of offering a button
  - a failed check (offline, rate limited) keeps the last answer and its time; nothing nags
- One tap updates the server, and on a Mac the app as well
  - `POST /update`, owner devices only, starts the update and returns at once; the app asks for its progress every two seconds until the server is back
  - it downloads the same files the install script uses, checks each against the release's SHA256SUMS, and stops with nothing changed if any of them fails
  - it runs the new binary once with `version` before swapping it in, so a build for the wrong machine never replaces a working one
  - the old binary is kept beside the new one as `heyrobotsd.previous`, for going back by hand
  - on a Mac it also replaces `heyrobots.app` where the install script put it (/Applications, then ~/Applications), with the same "downloaded" mark cleared; if no app is there, only the server updates
- The restart doesn't lose work
  - the server stops taking new work and waits up to two minutes for running tasks to finish
  - then it shuts down as on Ctrl+C and starts the new binary in its own place, as the same process, so launchd and systemd don't notice
  - a task still running after two minutes is interrupted, and picked up again after the restart as it is after any restart; the button says how many agents are working before you tap it
  - database changes in the new version run at its first start, as today; they can't be undone, which is why going back is by hand only
- The app shows it and comes back by itself
  - Settings gains an "Updates" row: "heyrobots 0.5.1 · up to date", or "0.5.2 is out" with the release notes and an "Update" button
  - the app asks when it starts and once a day while it stays open; when a newer one is out, the Settings button carries a dot
  - after you tap it, the app shows "Updating…", reconnects when the server answers again, and shows the new version
  - the Mac app whose bundle was replaced asks to relaunch ("Restart heyrobots to finish") and reopens itself
  - the web app comes inside the server, so it is new as soon as the server is back
  - phones only see "0.5.2 is out": Android gets a link to the new .apk; the server updating doesn't need the phone to update
  - a phone's app older than the server keeps working, since the API only grows

## Alternatives
- Run the install script from the server. It already does all of this, but it would mean fetching and running a shell script at runtime, and it stops and restarts the service from inside that service. Doing it in Go reuses the checksum rule and avoids both.
- The Mac app updates itself and then asks the server to update. The download and checksum code would live in both Dart and Go, and the web app and phones would still need the server to do it.
- Sparkle or another updater framework for the Mac app. It needs a signed app and an update feed, neither of which the project has; worth it once the app is signed by Apple.

## Behaviour
- Update tapped twice, or from two devices: the second gets "already updating".
- Download interrupted, or a checksum mismatch: nothing was replaced; the row says what failed and offers to try again.
- The new server fails to start: launchd or systemd keeps retrying it, and the app shows the server as unreachable. Going back is copying `heyrobotsd.previous` over `heyrobotsd`; the error the app shows says so.
- Several machines in a team cluster: each one updates itself from its own Settings; the update here doesn't touch the others.
