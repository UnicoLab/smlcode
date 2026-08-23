import { Modal } from './Modal';
import { SHORTCUTS } from '@/hooks/useKeyboard';

interface Props {
  open: boolean;
  onClose: () => void;
}

/** The `?` sheet. Groups come from the shortcut table itself. */
export default function ShortcutSheet({ open, onClose }: Props) {
  const groups = SHORTCUTS.reduce<Record<string, typeof SHORTCUTS>>((acc, s) => {
    (acc[s.group] ||= []).push(s);
    return acc;
  }, {});

  return (
    <Modal
      open={open}
      onClose={onClose}
      title="Keyboard shortcuts"
      description="Press ? to toggle this sheet, Esc to close it."
      className="max-w-2xl"
      footer={
        <button type="button" className="btn-secondary focus-ring text-xs" onClick={onClose}>
          Close
        </button>
      }
    >
      <div className="grid gap-6 sm:grid-cols-2">
        {Object.entries(groups).map(([group, items]) => (
          <section key={group}>
            <h3 className="mb-2 text-[10px] font-semibold uppercase tracking-wider text-gray-400">
              {group}
            </h3>
            <dl className="space-y-1.5">
              {items.map((s) => (
                <div key={s.keys} className="flex items-center justify-between gap-3">
                  <dd className="text-xs text-gray-600 dark:text-gray-300">{s.label}</dd>
                  <dt className="flex shrink-0 gap-1">
                    {s.keys.split(' ').map((k, i) => (
                      <kbd
                        key={`${s.keys}-${i}`}
                        className="rounded border border-gray-300 bg-gray-50 px-1.5 py-0.5 font-mono text-[10px]
                                   text-gray-700 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-200"
                      >
                        {k}
                      </kbd>
                    ))}
                  </dt>
                </div>
              ))}
            </dl>
          </section>
        ))}
      </div>
    </Modal>
  );
}
