import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import EntityLink from './EntityLink';
import { entityHref } from './entityHref';

function Where() {
  const loc = useLocation();
  return <output data-testid="where">{`${loc.pathname}${loc.search}`}</output>;
}

describe('EntityLink', () => {
  it('routes each kind to its page and parameter', () => {
    expect(entityHref('task', 'T4')).toBe('/?task=T4');
    expect(entityHref('task', 'T4', { team: 'backend-go' })).toBe('/?task=T4&team=backend-go');
    expect(entityHref('agent', 'go-worker')).toBe('/?agent=go-worker');
    expect(entityHref('team', 'backend-go')).toBe('/teams?team=backend-go');
    expect(entityHref('run', 'q_123')).toBe('/runs?run=q_123');
    expect(entityHref('file', 'web/src/App.tsx')).toBe('/files?file=web%2Fsrc%2FApp.tsx');
  });

  it('navigates inside the router', async () => {
    render(
      <MemoryRouter initialEntries={['/board']}>
        <Routes>
          <Route path="*" element={<><EntityLink kind="task" id="T4" params={{ team: 'backend-go' }} /><Where /></>} />
        </Routes>
      </MemoryRouter>,
    );
    const link = screen.getByRole('link', { name: /T4/ });
    expect(link).toHaveAttribute('href', '/?task=T4&team=backend-go');
    expect(link).toHaveAttribute('data-entity', 'task');
    await userEvent.click(link);
    expect(screen.getByTestId('where')).toHaveTextContent('/?task=T4&team=backend-go');
  });

  it('is a plain anchor to the same place outside a router', () => {
    render(<EntityLink kind="agent" id="go-worker" label="@go-worker" />);
    const a = screen.getByRole('link', { name: /@go-worker/ });
    expect(a).toHaveAttribute('href', '/?agent=go-worker');
    expect(a).toHaveAttribute('title', 'Agent go-worker — open');
  });
});
