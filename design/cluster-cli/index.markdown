---
co-review: design
---
# Manage the machines from any of them, and count each device once

## Context
Machines join as one team today only from the app: Settings → Your machines lists them, shows a join code, joins and removes. From a terminal you can't see the team at all, so a server with no app nearby can't be added or checked.

Devices are counted again each time they pair. Pairing again after a reinstall or a sign-out adds a new device and leaves the old one live, so this Mac's server lists two "Android phone", two "This Mac" and two "Browser on this machine", all still active.

More is coming for the cluster (an elected coordinator among them), so this keeps the team's logic in `internal/cluster` and adds only a thin way to reach it from the command line.

## Design
- Every machine can show and change the team from its own terminal
  - `heyrobotsd machines` lists the team, this machine first, each online or last seen, with its address and agent count
  - `heyrobotsd machines code` shows a join code; `machines join <address> <code>` joins another; `machines remove <name or id>` takes one out, and the removal spreads to the rest as it does from the app
  - `heyrobotsd status` also says how many machines are in the team and how many are online
  - the commands ask the running server, through a control socket in the data folder that only this user can open (`<data>/control.sock`, mode 0600); with no server running they say so
    - the socket gives the same access as reading the data folder, which this user has anyway
- The team's view is one function in the cluster package
  - what the app's machine list shows and what the terminal shows come from the same place, so an elected coordinator later changes it in one spot
  - the control socket is its own small file in the server's command, outside the HTTP API, so nothing about tokens or devices changes
- A device that pairs again replaces itself
  - the app keeps an install id that survives sign-out (a random id, kept apart from the pairing) and sends it when it pairs
  - pairing with an install id that is already live retires the older device, so the People screen shows each phone, Mac and browser once
  - a device paired before this has no install id; once, on upgrade, a device is retired if a newer one with the same name, kind and role paired after it was last seen, and it hasn't been seen for a day
    - two real phones with the same name both stay, since each is seen after the other paired

## Behaviour
- The app reinstalled and paired again: one device, the new one; the old token stops working, which it had already, since the app lost it.
- Two machines in the team: each keeps its own devices, as today. A phone paired to both shows once on each.
- `machines join` while the other machine is offline: it fails after 15 seconds with the reason, as in the app.
