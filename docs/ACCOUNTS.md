# Add another Claude or ChatGPT account

Open **Accounts** in the sidebar (on a phone, **More → Accounts**), then choose
the computer that will run your chats.

1. Choose **Add account** and select **Claude** or **ChatGPT / Codex**.
2. Optionally give it a nickname such as **Work**. Choose **Continue**.
3. Open the provider's sign-in page. Check which account you choose in your
   browser; it may remember the account you used last.
4. For ChatGPT, enter the displayed device code on the provider's page. Your
   ChatGPT account may require enabling device-code sign-in first. For Claude,
   paste the confirmation code back into Sessions.
5. Sessions displays the email and plan reported by the provider. Check the
   email, then choose **Done**.

Choose that account when starting a chat on this computer. Existing chats keep
their existing account; adding another account does not move or restart them.

Each account uses a separate provider configuration folder. The provider stores
and refreshes its credentials; Sessions stores only a nickname and the identity
reported at the last check. **Check account** checks that identity again without
logging an already connected account out. This is not a check of remaining usage.

Accounts are local to each computer. To use a subscription on another computer,
select that computer and sign in there too. Credentials are not copied through
the fleet. You can complete the browser steps from another device.

Each row leads with the account's nickname, or with the verified email when it
has none. **Verified** means the provider reported that identity at the date
shown; **Identity not checked** means Sessions has not asked the provider yet,
even if a login file is present, including after a removed account is added
again. When a newer check finds the account **Signed out** or **Not a
subscription** (for example an API-key login), or the **Check failed** because
Sessions could not read who is signed in, the earlier email is shown only as
"Last verified as …", never as the account signed in now. A failed check is not
a sign-out; check again. **Rename** changes only the nickname on that
computer (up to 64 characters; leave it empty to clear it). The account's
sign-in, history and chats are unchanged.

If sign-in expires or the host restarts, choose **Sign in** again. If you chose
the wrong browser account, add another account and select the intended login on
the provider's page. **Remove** unregisters an account from the list but preserves
its login and history; it does not end chats or cancel a subscription.

## Usage cap and switching accounts

Sessions keeps a run from using up an account: when any of its usage windows
(the 5-hour session window, the weekly limit, or a model's weekly limit) reaches
the usage cap, 90% unless you change it, Sessions stops starting new turns on
that account. A turn that is already running always finishes.

- A message to a chat on that account is not sent. It says which window is how
  full and when it resets, so you or the agent can send it again later.
- An idle chat on that account shows **Waiting for usage · resets 09:00**, with
  **switch to …?** when another signed-in account has room. Switching copies the
  conversation into a new chat on that account, like **Fork** does.
- A new chat started without choosing an account starts on the signed-in account
  of the same provider with the most room, and says so. If you chose the account
  yourself, or no account has room, the start is refused with the reading.
- A chat that stopped on a provider usage limit is continued on the account with
  the most room: Sessions copies its conversation into a new chat there and sends
  "Switched to account … Continue where you left off." once. The original chat
  is left as it was. Without another account it waits for the reset, as
  **Resume after usage limits** does, and never resumes while its account is
  still over the cap.

Switching happens only between accounts on the same computer. Credentials are
never copied; each account stays in its own provider folder. Readings are the
ones **Usage** shows, read at most once a minute; if Sessions has no current
reading, nothing is held back. The Accounts page draws the cap as a line on each
usage meter.

Change it in **Settings → Usage cap**, or with `sessions cap`: `sessions cap set
85`, `sessions cap set 95 --tool codex`, `sessions cap switch off`, `sessions cap
off`, or `sessions cap off --lane ID` for one chat.

The same operations are available through `sessions accounts`; see the
[CLI reference](CLI.md) and [HTTP contract](../runtime/CONTRACT/http-api.md).
