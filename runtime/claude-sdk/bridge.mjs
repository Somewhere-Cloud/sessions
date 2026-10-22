import { createInterface } from 'node:readline';
import { pathToFileURL } from 'node:url';
import { SDKSession } from './session.mjs';

// Private local stdio protocol. stdout is protocol only; provider stderr stays
// on stderr. Closing the runner pipe closes only this runner-owned SDK query.
const lines = createInterface({ input: process.stdin, crlfDelay: Infinity });
let session;
let outputOpen = true;
const emit = value => outputOpen && process.stdout.write(JSON.stringify(value) + '\n');
process.stdout.on('error', error => {
  if (error.code !== 'EPIPE') throw error;
  outputOpen = false;
  // The owning runner disappeared; stop only this bridge's SDK query.
  lines.close();
  void session?.close();
});
for await (const line of lines) {
  let command;
  try {
    command = JSON.parse(line);
    if (command.type === 'init') {
      if (session) throw new Error('SDK session already initialized');
      const { query } = process.argv[2]
        ? await import(pathToFileURL(process.argv[2]).href)
        : await import('@anthropic-ai/claude-agent-sdk');
      const env = { ...process.env };
      delete env.ANTHROPIC_API_KEY;
      delete env.CLAUDECODE;
      session = new SDKSession(query, { ...command.options, env,
        stderr: text => process.stderr.write(text) }, emit);
      emit({ type: 'initialized' });
    } else if (command.type === 'submit') {
      if (!session) throw new Error('Initialize the SDK session first');
      if (command.settings) await session.provider.applyFlagSettings(command.settings);
      session.submit(command.operation_id, command.text);
    } else if (command.type === 'interrupt') {
      if (!session) throw new Error('Initialize the SDK session first');
      await session.interrupt(command.operation_id);
    } else if (command.type === 'close') {
      break;
    } else {
      throw new Error('Unknown SDK bridge command');
    }
  } catch (error) {
    emit({ type: 'command_error', operation_id: command?.operation_id ?? null,
      error: String(error.message || error) });
  }
}
await session?.close();
