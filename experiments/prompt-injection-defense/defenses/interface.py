# defenses/interface.py
from dataclasses import dataclass


@dataclass
class DefenseResult:
    detected: bool
    explanation: str
    raw_output: str | None = None


@dataclass
class Attack:
    name: str
    description: str
    target_defense: str
    commit_message: str
    injection_goal: str
