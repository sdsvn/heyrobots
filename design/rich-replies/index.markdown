---
co-review: design
---
# Show replies with formatting, and images and PDFs inside the chat

## Context
The app draws every reply as plain text, so an agent's markdown shows up as raw `**` and `-`. Agents write markdown anyway, and the live Go-release answer showed the problem. Files an agent shares with `share_file` appear only as a tile with a Download button: you can't see a photo or a PDF without downloading it and opening it somewhere else.

This changes only the app: the chat screen's bubbles in `app/lib/ui/chat_screen.dart` and the older thread screen, plus the file tile in `chat_kit.dart`. The server and the API stay as they are, except for two steps: email replies gain an HTML version, and the A2UI step adds a tool and a reply part kind.

## Design
- Agents' replies render as markdown
  - uses `flutter_markdown_plus` 1.0.12, the maintained successor of the Flutter team's discontinued `flutter_markdown`; it is a new dependency
  - covers what agents actually write: headings, bold and italic, bullet and numbered lists, quotes, inline code and code blocks, tables, links and horizontal rules
  - styled from the app's Palette and Nunito, so it matches the existing bubbles in light and dark; code uses a monospace font on the ground colour
  - text stays selectable, as it is today
  - your own messages stay plain text, exactly as you typed them
  - what can go wrong: a long table or code block is wider than the bubble, so it scrolls sideways inside the bubble instead of stretching it
- Links open outside the app, and say where they go
  - tapping a link opens it in the browser, as file downloads already do
  - a link whose visible text differs from where it goes shows the real address before it opens, since replies can carry text from web pages
- Images an agent shares show in the chat
  - a shared file whose type is an image shows as a picture in the bubble, at most the bubble's width and 320 points tall
  - it loads through the same short-lived file link the Download button uses, so nothing new is exposed and the server needs no change
  - tapping it opens the picture full screen, where you can zoom; the Download button stays under it
  - if it fails to load (the link expired, the file is gone), it falls back to today's file tile
- Images in markdown load from the web
  - `![alt](https://…)` shows the picture inline, sized like a shared image, and tapping it opens it full screen
  - only `https` addresses load; anything else, or a picture that fails to load, shows its alt text as a link
  - what can go wrong: loading tells that website the message was read, and a reply can carry an address copied from an untrusted page (the owner accepted this); in the browser app, a site that doesn't allow cross-origin loading shows the link fallback
- PDFs an agent shares show a preview
  - the chat shows the first page as a picture, with the page count and the Download button
  - tapping it opens a viewer inside the app with every page, zoom and scrolling
  - uses `pdfrx` 2.12.1, a new dependency that renders PDFs the same way on iPhone, Android, Mac and the web; on the web it brings its PDF engine as a WebAssembly file
  - a PDF that won't open (damaged, or protected by a password) falls back to the file tile
- Every other file stays a tile, as today, including videos and audio
- Replies sent by email are formatted too
  - the mail composer in `internal/sources/mail` sends each reply as both plain text (the markdown as written, which still reads well) and HTML rendered from it, so mail apps show headings, lists, tables and code
  - renders with `goldmark` v1.8.7, a new Go dependency, with tables and autolinks on; raw HTML inside a reply is dropped, never passed through, since replies can carry text from web pages
  - styles are inline and plain (system font, no remote stylesheets), because most mail apps drop anything else
  - what can go wrong: web images in the HTML are left to the mail app, which usually blocks them until the reader allows it; A2UI surfaces can't run in email, so their text fallback is sent instead
- Agents can reply with native UI as well as text, using A2UI
  - a new `show_ui` tool lets an agent send an A2UI surface (protocol v1.0: a flat list of components such as cards, rows, text, images, buttons and text fields); it is stored as a new reply part kind next to text, files and cards, so this step does change the server and the API
  - the app renders it with `a2ui_flutter` 0.3.0 and `a2ui_core` 0.7.0 (the successors of Flutter's `genui`) using the basic catalog without its video and audio players, themed from the Palette and Nunito so it looks like the rest of the chat
  - tapping a button sends its action back to the agent as a message in the same chat
  - the server checks each component against the catalog's own schema before storing it and tells the agent what to fix; one the app still can't draw shows as the agent's plain text fallback instead
  - today's simple cards stay as they are in this change and move onto A2UI in a follow-up, so later there is one way to show UI
  - what can go wrong: images inside a surface follow the same rule as markdown images (shared files and `https` addresses load); A2UI is young (0.x), so a breaking release means pinning and updating both packages together

## Alternatives
- Write a small markdown renderer by hand. It would cover bold and lists, but not tables or code blocks, and it would become ours to maintain.
- Open PDFs in the browser's own viewer instead of `pdfrx`. That needs no dependency, but the server would have to serve PDFs for viewing instead of downloading, and drop the sandbox rule that keeps a file from running anything, because that rule breaks Chrome's PDF viewer. On phones it would leave the app.
- Tell agents to write plain text. Formatting helps for longer answers, and the app is the only place they mostly write to.

