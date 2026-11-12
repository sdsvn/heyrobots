# Connecting Gmail and Google Calendar

heyrobots talks to Google through **your own** Google app, so your mail goes only between Google and your own server. Setting it up takes about ten minutes. The app shows the same steps under Settings → Connected accounts.

## 1. Create the Google app

1. Open [console.cloud.google.com](https://console.cloud.google.com) and create a project, for example "heyrobots".
2. Under **APIs & Services → Library**, enable the **Gmail API** and the **Google Calendar API**.
3. Under **OAuth consent screen**, choose **External**. Fill in the app name and your own address, and add yourself as a test user.
4. **Publish the app** ("In production"). While an app is in "Testing", Google ends its sign-ins after 7 days.
   - You don't need Google's verification. An app that isn't shared, or is used by fewer than 100 people the developer knows personally, is exempt under Google's [personal-use exception](https://support.google.com/cloud/answer/13464323). That holds even for the Gmail scopes.
   - The cost of skipping verification is a screen saying ["Google hasn't verified this app"](https://support.google.com/cloud/answer/7454865). Click **Advanced**, then **Go to heyrobots**. It's your own app.
   - The 100-person limit covers the project's whole lifetime. That is plenty for you and a few delegates.
5. Under **Credentials**, create an **OAuth client ID** of type **Desktop app**. A Desktop app client lets heyrobots receive the sign-in on your own computer, at `http://127.0.0.1`, with nothing to register.
6. Copy the **client ID** and **client secret** into the app. They are stored encrypted on your server.

## 2. Connect your account

In the app, tap **Connect Google**.
- **Allow sending mail and changing my calendar** adds the permission to send and to change events. Leave it off if you only want reading and drafts; you can turn it on later by connecting again. Even with it on, anything that reaches other people waits for your approval, unless a standing approval you created covers it.
- **Address tag** picks the Assistant's address. The default `assistant` makes it `you+assistant@gmail.com`.

Your browser opens Google's sign-in.
- **On the computer running heyrobots**, the connection finishes by itself.
- **On a phone or another computer**, the browser ends on a page that can't load, at an address starting with `http://127.0.0.1`. That is expected. Copy the whole address, paste it into the app, and tap **Finish**.

## What heyrobots does with the account

- **Your mail to the Assistant is a request.** Mail you send from your own address to `you+assistant@…` is a request, just like the app chat. heyrobots checks that it really came from you: the message must carry a valid DKIM signature from your address's domain, or Gmail must have filed it as sent from this very account. A `From:` line alone is never trusted, and mail from anyone else to that address is ignored.
- **Other mail in your inbox** is never a request. It can only start work through a proactive rule you added, and in observe mode, which is the default, the Assistant only notes what it would have done. While you have no rule enabled, inbox mail costs no model calls at all.
- **Calendar.** Invitations you haven't answered, and meetings with other people starting in about 30 minutes, reach the Assistant the same way as inbox mail: only through your rules.
- **Reading.** Mail and invitations from other people are read in quarantine: a separate model call with no tools that pulls out what a task needs. Their text can't give the Assistant instructions.
- **Drafts** are saved in your mailbox and reach nobody.
- **Sending** a message, creating an event with attendees and answering an invitation all go through the outbox. Anything that reaches someone else waits for your approval, which a phone confirms with Face ID or a fingerprint.
- Your Google sign-in is encrypted on your server, and the model never sees it. **Disconnect** in the app revokes it with Google.

## If something goes wrong

- **"Google no longer accepts the sign-in"**: the app was left in Testing, or you removed heyrobots from your Google account's [third-party access](https://myaccount.google.com/connections). Publish the app, then connect again.
- **"google gave no refresh token"**: remove heyrobots under third-party access, then connect again.
- **Sending fails with "needs the extra permission"**: connect again with sending allowed.
