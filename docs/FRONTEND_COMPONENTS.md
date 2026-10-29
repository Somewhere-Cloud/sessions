# Shared frontend components

Sessions controls live in `frontend/src/components/ui`. Use them for new and
migrated surfaces. Keep domain choices, requests, and operation state in the
caller. Existing screens can migrate incrementally; importing the shared CSS
does not reset their layout. Sessions keeps native HTML inputs/selects and its
existing React 18 and CSS toolchain.

## Controls

`Button` and `IconButton` in `ui/Button.tsx` forward a native button ref and
native attributes. `Button` defaults to `type="button"`; explicitly choose
`type="submit"` in forms. Variants are `secondary` (default), `primary`, `ghost`,
and `danger`; sizes are `default` and `compact`. Use one primary action per
review. Danger styling belongs to consequential actions, not offline or finished
work. `IconButton` requires `aria-label` and hides its decorative child from
assistive technology. Essential instructions belong in visible text, not `title`.

`loading` disables activation and adds `aria-busy`. Supply meaningful visible
copy such as “Saving…”; the component does not invent domain progress or start
an operation. `disabled` uses native button behavior. Both controls preserve
keyboard activation, focus-visible outlines, and 44px coarse-pointer targets.
No polymorphic `as` prop or link emulation is supported; navigation uses links.

```tsx
import { Button, IconButton } from './ui/Button';

<IconButton aria-label="Close review" disabled={saving} onClick={close}>×</IconButton>
<Button variant="primary" loading={saving} onClick={save}>
  {saving ? 'Saving…' : 'Save changes'}
</Button>
```

## Fields

`Field`, `Input`, `Select`, and `Textarea` live in `ui/Field.tsx`. Put exactly one
labelable shared control inside each Field. Field generates a stable ID with
React `useId`, associates its visible label, help, and error, and supplies
`aria-invalid` when an error exists. If an explicit ID is needed, set it on the
Field; any explicit control ID must match. Native attributes and refs are
forwarded. Additional `aria-describedby` IDs are appended to the field's help
and error IDs. Native `required`, validation, disabled, selection, and mobile
select behavior remain available. These wrappers do not validate domain data.

```tsx
<Field label="Project name" hint="Shown in your navigator." error={error}>
  <Input value={name} onChange={(event) => setName(event.target.value)} />
</Field>
<Field label="Computer">
  <Select value={computer} onChange={(event) => setComputer(event.target.value)}>
    <option value="local">This computer</option>
  </Select>
</Field>
```

For groups of checkboxes/radios, use native fieldset/legend and established
specialized controls. Do not wrap a group in one Field or replace ModelPicker's
search/list behavior with a styled native select.

## Layout and states

`ui/Layout.tsx` supplies `Surface` (bordered card), `Stack` (vertical rhythm),
`Actions` (wrapping, trailing actions), `PageHeader`, `Section`, `EmptyState`, and
`Status`. Surface/Stack forward div attributes; Actions forwards footer
attributes. PageHeader accepts `title`, optional `titleId`, `description`,
`eyebrow`, `actions`, and heading `level` (1/2/3). A dialog normally uses level 2.
Section defaults to level 2 and creates a labelled section. Choose levels to
match the surrounding document, rather than to obtain a visual size.

EmptyState accepts `title`, explanatory children, and optional `action`.
Status accepts textual children, tone `neutral`/`success`/`warning`/`danger`, and
`live="polite"` (default), `"assertive"`, or `"off"`. Tone changes the border,
not message meaning or announcement priority. Use assertive only for an error
requiring immediate attention; static information should normally use off.
Do not announce every streamed token or replace a provider-specific recovery
card with a generic error. Always explain the failed operation and safe next
step in the caller's copy.

```tsx
<Surface>
  <Section title="Project details" description="Names and tags stay yours.">
    <Field label="Name"><Input value={name} onChange={changeName} /></Field>
    <Actions><Button onClick={close}>Cancel</Button><Button variant="primary">Save</Button></Actions>
  </Section>
</Surface>
```

## Tokens, themes, motion, and dialogs

`styles/sections/00-foundation.css` remains the color, radius, shadow, and font
source. The light theme overrides those same tokens in
`40-operations-workspace.css`. `styles/ui.css` adds shared spacing, typography,
control/icon sizing, touch targets, and motion tokens. New primitives consume
these tokens; avoid per-screen palettes or a second density setting. Shared
styles are opt-in `ui-*` classes layered over established `.btn` and
`.field-input` rules. Labels stay at least 12px; compact size never reduces a
coarse-pointer control below 44px. Reduced motion removes shared control
transitions and press transforms. Existing legacy animations are separate.

Reuse `ModalDialog`, including its focus containment, dismissal lock, owned
portals, and focus return. Mount it only while open, provide a unique
`labelledBy` target, and preserve each operation's busy and backdrop policy.
It follows operations-shell theme token changes even under `document.body`.
Body-portaled shared dialogs apply shell scaling once and constrain their size
to the viewport; inline dialogs already inherit shell scaling. The hook observes
the single shell's class, style, and theme attributes. It is not a theme store.
Other legacy body portals do not automatically receive this contract.

## Examples and verification

`frontend/scripts/ui-foundation-fixture.tsx` is a test-only composed gallery
using the real controls, TagEditor, Add agent, and Restart dialogs with an
in-process fake daemon. It is not a shipped navigation destination. Run:

```sh
cd frontend
npm run test:ui-foundation
UI_SCREENSHOT_DIR=/absolute/existing/directory npm run test:ui-foundation
npm run test:capability -- tests/capability/ui-foundation.test.tsx
```

The rendered smoke checks dark/light, desktop/375px, coarse targets, large-scale
portals, live theme updates, reduced motion, and cancellation without mutations.
Screenshots are optional local artifacts. The capability tests cover labels,
help/errors, refs, keyboard activation, disabled/loading, tag validation, and
status semantics. Existing dialog tests cover focus, nested model search,
Escape, dismissal locks, and operation payloads. Chromium fixtures do not prove
native WebKit, VoiceOver/TalkBack, or installed-client acceptance.

When migrating a surface, preserve domain handlers and semantics, choose these
shared compositions, remove its superseded selectors, and run the relevant
behavior tests plus rendered narrow/theme fixtures. Do not move operational
authority into a visual component. Keep CLI/JSON parity for new operations and
keep entry/deferred bundles within the existing limits.

## Shell compositions

Home composes PageHeader, Section, Surface, Stack and shared buttons. Daily
uses the same heading, state and control APIs while keeping its activity data
and timeline. Product navigation, project controls and launcher selections use
shared native controls. CommandPalette and mobile More use ModalDialog for
focus containment and focus return. Keep the mobile More heading wrapper:
Android Back finds its close button through that selector.

The workspace EmptyState supports both first use and an unselected session;
`hasSessions` changes its invitation without creating or resuming work. The
launcher keeps its specialized model picker, draft and creation handlers.
Navigator rows, menus, terminal/conversation content, search/history, settings,
accounts and other unmigrated surfaces still have specialized or legacy styles.
Shared controls do not imply that their entire containing screen has migrated.

`npm run test:shell-ui` renders production shell components with an in-process
fake daemon at desktop and 375px in both themes, populated and empty. It checks
filtering, modal keyboard focus and return, bounds and navigation without
operational mutations. Set `UI_SCREENSHOT_DIR` to an existing absolute directory
to capture the composed screens and Daily loading/error states. It is part of
the complete smoke gate. Native platform and assistive-technology acceptance
remain separate from these Chromium fixtures.

## Conversation compositions

Conversation headers, terminal toolbar actions, composer controls, delivery
reviews, permission choices and recovery actions use shared buttons. Session
Details composes Surface/Section and an associated end-reason Field. The
composer uses the shared Textarea while retaining durable drafts, attachment
insertion, IME handling and acknowledged send/queue behavior. Model/effort
review uses ModalDialog with the existing specialized ModelPicker, including
its owned portal. Usage continues to compose ComposerAccountUsage.

Messages retain the specialized RemoteView scroll/streaming implementation
and WorkLog activity disclosures. Prose has a bounded reading column; wide
code and tables scroll locally. Specialized renderers, xterm, native terminal
sizing and provider operation handlers remain separate from presentation.

`npm run test:conversation-ui` renders production conversation components with
an in-process fake daemon in both themes at desktop and 375px, plus enlarged
text. It exercises scroll anchors, model-dialog focus return, attachment
insertion without sending, failed-send draft retention and permission choices.
Set `UI_SCREENSHOT_DIR` to an existing absolute directory to retain screenshots.
The capability suite additionally checks IME/multiline sends and exact approval
and model payloads. These Chromium fixtures do not establish native WebKit,
installed-client, device or screen-reader acceptance.
