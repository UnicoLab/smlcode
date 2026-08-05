import { useState } from 'react';
import type { StackPreset } from '@/types';
import clsx from 'clsx';

const STACKS: StackPreset[] = [
  {
    id: 'omlx-local',
    label: 'oMLX Local',
    description: 'Apple MLX on Mac — ultra-light local SLM',
    icon: '🍎',
    provider: 'omlx',
    endpoint: 'http://127.0.0.1:8000/v1',
    model: 'Qwen3-Coder-30B-A3B-Instruct-MLX-4bit',
    temperature: 0.12,
    max_tokens: 3072,
    max_parallel: 2,
    max_retries: 4,
    max_context_kb: 32,
    think_passes: 1,
    backend: 'slmcode',
    color: 'from-violet-500 to-purple-600',
    active: true,
  },
  {
    id: 'deepseek',
    label: 'DeepSeek',
    description: 'DeepSeek V3/R1 — affordable reasoning',
    icon: '🐋',
    provider: 'deepseek',
    endpoint: 'https://api.deepseek.com/v1',
    model: 'deepseek-chat',
    temperature: 0.15,
    max_tokens: 4096,
    max_parallel: 3,
    max_retries: 3,
    max_context_kb: 64,
    think_passes: 2,
    backend: 'slmcode',
    env_key: 'DEEPSEEK_API_KEY',
    color: 'from-blue-500 to-cyan-600',
    active: false,
  },
  {
    id: 'qwen',
    label: 'Qwen',
    description: 'Qwen 2.5 via OpenRouter',
    icon: '🐉',
    provider: 'openrouter',
    endpoint: 'https://openrouter.ai/api/v1',
    model: 'qwen/qwen-2.5-coder-32b-instruct',
    temperature: 0.15,
    max_tokens: 4096,
    max_parallel: 2,
    max_retries: 3,
    max_context_kb: 64,
    think_passes: 2,
    backend: 'slmcode',
    env_key: 'OPENROUTER_API_KEY',
    color: 'from-teal-500 to-emerald-600',
    active: false,
  },
  {
    id: 'google',
    label: 'Google Gemini',
    description: 'Gemini 2.5 Pro — long context',
    icon: '💎',
    provider: 'google',
    endpoint: 'https://generativelanguage.googleapis.com/v1beta/openai/',
    model: 'gemini-2.0-flash',
    temperature: 0.15,
    max_tokens: 4096,
    max_parallel: 3,
    max_retries: 3,
    max_context_kb: 128,
    think_passes: 2,
    backend: 'slmcode',
    env_key: 'GOOGLE_API_KEY',
    color: 'from-blue-600 to-indigo-700',
    active: false,
  },
  {
    id: 'openai',
    label: 'OpenAI',
    description: 'GPT-4o — maximum capability',
    icon: '⚡',
    provider: 'openai',
    endpoint: 'https://api.openai.com/v1',
    model: 'gpt-4o',
    temperature: 0.15,
    max_tokens: 4096,
    max_parallel: 4,
    max_retries: 3,
    max_context_kb: 128,
    think_passes: 2,
    backend: 'slmcode',
    env_key: 'OPENAI_API_KEY',
    color: 'from-emerald-500 to-green-600',
    active: false,
  },
  {
    id: 'ollama-local',
    label: 'Ollama Local',
    description: 'Run models locally via Ollama',
    icon: '🦙',
    provider: 'ollama',
    endpoint: 'http://127.0.0.1:11434',
    model: 'qwen2.5-coder:14b',
    temperature: 0.15,
    max_tokens: 3072,
    max_parallel: 2,
    max_retries: 3,
    max_context_kb: 32,
    think_passes: 1,
    backend: 'slmcode',
    color: 'from-amber-500 to-orange-600',
    active: false,
  },
  {
    id: 'openrouter',
    label: 'OpenRouter',
    description: 'Any model via OpenRouter proxy',
    icon: '🌐',
    provider: 'openrouter',
    endpoint: 'https://openrouter.ai/api/v1',
    model: 'anthropic/claude-sonnet-4',
    temperature: 0.15,
    max_tokens: 4096,
    max_parallel: 3,
    max_retries: 3,
    max_context_kb: 128,
    think_passes: 2,
    backend: 'slmcode',
    env_key: 'OPENROUTER_API_KEY',
    color: 'from-rose-500 to-pink-600',
    active: false,
  },
  {
    id: 'groq',
    label: 'Groq',
    description: 'Fast inference via Groq LPU',
    icon: '⚙️',
    provider: 'groq',
    endpoint: 'https://api.groq.com/openai/v1',
    model: 'llama-3.3-70b-versatile',
    temperature: 0.15,
    max_tokens: 4096,
    max_parallel: 4,
    max_retries: 3,
    max_context_kb: 128,
    think_passes: 2,
    backend: 'slmcode',
    env_key: 'GROQ_API_KEY',
    color: 'from-orange-500 to-red-600',
    active: false,
  },
];

interface StackSelectorProps {
  current: { provider: string; model: string; endpoint: string };
  onSelect: (preset: StackPreset) => void;
}

export default function StackSelector({ current, onSelect }: StackSelectorProps) {
  const [selected, setSelected] = useState<string | null>(null);

  const isActive = (stack: StackPreset) =>
    current.provider === stack.provider && current.model === stack.model;

  return (
    <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
      {STACKS.map((stack) => {
        const active = isActive(stack);
        const sel = selected === stack.id;
        return (
          <button
            key={stack.id}
            onClick={() => {
              setSelected(stack.id);
              onSelect(stack);
              setTimeout(() => setSelected(null), 600);
            }}
            className={clsx(
              'relative group flex flex-col items-center gap-3 p-4 rounded-xl border-2 transition-all duration-200',
              'hover:shadow-lg hover:-translate-y-0.5',
              sel && 'scale-95',
              active
                ? 'border-brand-500 bg-brand-50 dark:bg-brand-900/20 shadow-md'
                : 'border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-900 hover:border-brand-300 dark:hover:border-brand-700',
            )}
          >
            {/* Active indicator */}
            {active && (
              <div className="absolute top-2 right-2 w-2 h-2 rounded-full bg-emerald-500 ring-2 ring-white dark:ring-gray-900 animate-pulse" />
            )}

            {/* Icon */}
            <div
              className={clsx(
                'w-12 h-12 rounded-xl bg-gradient-to-br flex items-center justify-center text-2xl shadow-sm',
                stack.color,
              )}
            >
              <span className="drop-shadow-sm">{stack.icon}</span>
            </div>

            {/* Label */}
            <div className="text-center">
              <div className={clsx(
                'text-sm font-bold',
                active ? 'text-brand-700 dark:text-brand-300' : 'text-gray-700 dark:text-gray-300',
              )}>
                {stack.label}
              </div>
              <div className="text-[10px] text-gray-400 mt-0.5 line-clamp-2">{stack.description}</div>
            </div>

            {/* Model name */}
            <div className="text-[9px] font-mono text-gray-400 dark:text-gray-600 truncate w-full text-center px-1">
              {stack.model}
            </div>

            {/* Provider badge */}
            <span className={clsx(
              'badge text-[9px]',
              active ? 'badge-brand' : 'badge-neutral',
            )}>
              {stack.provider}
            </span>

            {/* Hover outline */}
            <div className={clsx(
              'absolute inset-0 rounded-xl opacity-0 group-hover:opacity-100 transition-opacity duration-300 pointer-events-none',
              'bg-gradient-to-r from-transparent via-brand-400/10 to-transparent',
            )} />
          </button>
        );
      })}
    </div>
  );
}
