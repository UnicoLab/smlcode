"""Service module."""
from __future__ import annotations

import os
from dataclasses import dataclass


@dataclass
class Config:
    """Runtime config."""

    path: str


class Service:
    """Main service."""

    def __init__(self, config: Config) -> None:
        self.config = config

    def start(self) -> bool:
        return os.path.exists(self.config.path)

    def _private(self) -> None:
        pass


def build_service(path: str) -> Service:
    return Service(Config(path=path))


async def shutdown(service: Service) -> None:
    del service
