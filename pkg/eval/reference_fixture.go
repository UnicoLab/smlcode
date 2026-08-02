package eval

import (
	"os"
	"path/filepath"
)

// WriteLangGraphReferenceScaffold writes an expert-quality LangGraph class-agent
// template into root. This is the bar the engine must match or beat.
func WriteLangGraphReferenceScaffold(root string) error {
	files := map[string]string{
		"requirements.txt": "langgraph>=0.2\nlangchain-core>=0.2\npytest>=8.0\n",
		"main.py": `"""Demo entrypoint for the class-based LangGraph agent template."""
from __future__ import annotations

from src.lg_agent.agents.base import EchoAgent


def main() -> None:
    agent = EchoAgent()
    result = agent.invoke({"messages": ["hello from main"]})
    print(result)


if __name__ == "__main__":
    main()
`,
		"src/lg_agent/__init__.py": `"""LangGraph class-agent template package."""
__all__ = ["agents", "chains", "prompts", "memory", "tools", "config"]
`,
		"src/lg_agent/state.py": `from __future__ import annotations

from typing import Annotated, TypedDict

from langgraph.graph.message import add_messages


class AgentState(TypedDict):
    messages: Annotated[list, add_messages]
`,
		"src/lg_agent/agents/__init__.py": `from .base import BaseAgent, EchoAgent

__all__ = ["BaseAgent", "EchoAgent"]
`,
		"src/lg_agent/agents/base.py": `from __future__ import annotations

from typing import Any

from langgraph.graph import END, StateGraph

from src.lg_agent.state import AgentState


class BaseAgent:
    """Class-based LangGraph agent: build → compile → invoke."""

    def __init__(self) -> None:
        self._graph = self.build_graph().compile()

    def build_graph(self) -> StateGraph:
        raise RuntimeError("subclasses must implement build_graph")

    def invoke(self, inputs: dict[str, Any]) -> dict[str, Any]:
        return self._graph.invoke(inputs)


class EchoAgent(BaseAgent):
    def build_graph(self) -> StateGraph:
        g = StateGraph(AgentState)

        def echo_node(state: AgentState) -> dict[str, Any]:
            msgs = list(state.get("messages") or [])
            msgs.append("echo:ok")
            return {"messages": msgs}

        g.add_node("echo", echo_node)
        g.set_entry_point("echo")
        g.add_edge("echo", END)
        return g
`,
		"src/lg_agent/chains/__init__.py": `from .factory import build_echo_chain

__all__ = ["build_echo_chain"]
`,
		"src/lg_agent/chains/factory.py": `from __future__ import annotations

from langchain_core.prompts import ChatPromptTemplate
from langchain_core.runnables import RunnableLambda


def build_echo_chain():
    prompt = ChatPromptTemplate.from_messages([
        ("system", "You are a helpful template agent."),
        ("human", "{text}"),
    ])
    return prompt | RunnableLambda(lambda x: {"text": str(x)})
`,
		"src/lg_agent/prompts/__init__.py": `from .templates import SYSTEM_PROMPT

__all__ = ["SYSTEM_PROMPT"]
`,
		"src/lg_agent/prompts/templates.py": `SYSTEM_PROMPT = "You are a scalable LangGraph agent. Prefer tools and structured state."
`,
		"src/lg_agent/memory/__init__.py": `from .store import InMemoryStore

__all__ = ["InMemoryStore"]
`,
		"src/lg_agent/memory/store.py": `from __future__ import annotations


class InMemoryStore:
    def __init__(self) -> None:
        self._data: dict[str, str] = {}

    def put(self, key: str, value: str) -> None:
        self._data[key] = value

    def get(self, key: str, default: str = "") -> str:
        return self._data.get(key, default)
`,
		"src/lg_agent/tools/__init__.py": `from .registry import TOOLS, echo_tool

__all__ = ["TOOLS", "echo_tool"]
`,
		"src/lg_agent/tools/registry.py": `from __future__ import annotations

from langchain_core.tools import tool


@tool
def echo_tool(text: str) -> str:
    """Echo input text (template tool)."""
    return text


TOOLS = [echo_tool]
`,
		"src/lg_agent/config/__init__.py": `from .settings import Settings

__all__ = ["Settings"]
`,
		"src/lg_agent/config/settings.py": `from __future__ import annotations

from dataclasses import dataclass


@dataclass
class Settings:
    model_name: str = "template-model"
    temperature: float = 0.0
`,
		"tests/test_smoke.py": `from __future__ import annotations

from pathlib import Path

from src.lg_agent.agents.base import EchoAgent

# Avoid writing banned stub phrases literally in this file (static gate scans tests too).
_BAD_STUB = "Place" + "holder implementation"
_BAD_IMPORT = "from langgraph import " + "Graph"


def test_agent_invoke():
    agent = EchoAgent()
    out = agent.invoke({"messages": ["hi"]})
    assert out.get("messages")
    assert "echo:ok" in out["messages"]


def test_no_stub_markers():
    root = Path(__file__).resolve().parents[1]
    for path in root.joinpath("src").rglob("*.py"):
        text = path.read_text(encoding="utf-8")
        assert _BAD_STUB not in text
        assert _BAD_IMPORT not in text
`,
	}
	for rel, body := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// WriteLangGraphGarbageFixture recreates the TestSLMs-style failure mode.
func WriteLangGraphGarbageFixture(root string) error {
	files := map[string]string{
		"src/lg_agent/__init__.py":        "",
		"src/lg_agent/agents/__init__.py": "",
		"src/lg_agent/agents/agent.py": "from langgraph import Graph\nfrom typing import Any, Dict\n\n" +
			"class BaseAgent:\n" +
			"    def run(self, input_data: Dict[str, Any]) -> Dict[str, Any]:\n" +
			"        # Placeholder implementation\n" +
			"        return {\"output\": \"run_result\"}\n",
		"src/lg_agent/chains/__init__.py":  "",
		"src/lg_agent/prompts/__init__.py": "",
		"src/lg_agent/memory/__init__.py":  "",
	}
	for rel, body := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}
