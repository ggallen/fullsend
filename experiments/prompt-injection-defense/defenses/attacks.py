# defenses/attacks.py
from pathlib import Path

import yaml

from defenses.interface import Attack

REQUIRED_FIELDS = ("name", "description", "target_defense", "commit_message", "injection_goal")


def load_attack(path: Path) -> Attack:
    with open(path) as f:
        data = yaml.safe_load(f)

    for field in REQUIRED_FIELDS:
        if not data or field not in data:
            raise ValueError(f"Attack file {path} missing required field: {field}")

    return Attack(
        name=data["name"],
        description=data["description"],
        target_defense=data["target_defense"],
        commit_message=data["commit_message"],
        injection_goal=data["injection_goal"],
    )


def load_all_attacks(directory: Path) -> list[Attack]:
    attacks = []
    for path in sorted(directory.glob("*.yaml")):
        attacks.append(load_attack(path))
    return attacks
