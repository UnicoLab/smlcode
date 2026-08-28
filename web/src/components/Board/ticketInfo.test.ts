import { describe, expect, it } from 'vitest';
import { ticketInfo } from './ticketInfo';

describe('ticketInfo', () => {
  it('leaves ordinary planned work alone', () => {
    expect(ticketInfo({ notes: '' })).toEqual({ isTicket: false, attempt: 0, reassignedTo: undefined });
    expect(ticketInfo({ notes: 'scoped by the splitter' }).isTicket).toBe(false);
    expect(ticketInfo({}).isTicket).toBe(false);
  });

  it('recognizes a correction ticket', () => {
    const notes = [
      'correction ticket from the tester gate; assigned to go-worker',
      'correction-key: tester|boom|cmd/server/main.go',
      'correction-attempt: 1',
    ].join('\n');
    expect(ticketInfo({ notes })).toEqual({
      isTicket: true,
      attempt: 1,
      reassignedTo: undefined,
    });
  });

  // The two things somebody managing the board needs at a glance: this is a
  // rework, and somebody else has it now.
  it('reads the attempt and who the manager moved it to', () => {
    const notes = [
      'correction-key: tester|boom|cmd/server/main.go',
      'correction-attempt: 2',
      'reassigned-to: go-corrector (repeat ticket, routed by the project manager)',
    ].join('\n');
    const got = ticketInfo({ notes });
    expect(got.attempt).toBe(2);
    expect(got.reassignedTo).toBe('go-corrector');
  });

  // A marker that landed at the start of an empty notes field loses its leading
  // newline to any caller that trims — the harness reads it back the same way.
  it('reads a marker on the first line', () => {
    expect(ticketInfo({ notes: 'correction-key: tester|boom|a.go' }).isTicket).toBe(true);
  });

  // Ticket bodies quote their own markers back when an agent describes what it
  // did, and a mention is not a stamp.
  it('does not read a marker mentioned mid-line', () => {
    expect(ticketInfo({ notes: 'see correction-key: x for context' }).isTicket).toBe(false);
  });

  it('ignores an unparsable attempt', () => {
    const notes = 'correction-key: k\ncorrection-attempt: soon';
    expect(ticketInfo({ notes }).attempt).toBe(0);
  });
});
