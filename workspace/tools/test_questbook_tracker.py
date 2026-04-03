import unittest
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent))

import questbook_tracker as qt


class ParseSNBTTests(unittest.TestCase):
    def test_parse_basic_object(self) -> None:
        doc = qt.parse_snbt(
            """
            {
              title: "Hello"
              values: [1 2 3]
              nested: { enabled: true ratio: 1.5d }
            }
            """
        )
        self.assertEqual(doc["title"], "Hello")
        self.assertEqual(doc["values"], [1, 2, 3])
        self.assertTrue(doc["nested"]["enabled"])
        self.assertEqual(doc["nested"]["ratio"], 1.5)

    def test_clean_text_removes_minecraft_formatting(self) -> None:
        self.assertEqual(qt.clean_text("&6Hello &r&#FF00FFWorld"), "Hello World")


class QuestPayloadTests(unittest.TestCase):
    def test_build_chapter_payload_marks_completion(self) -> None:
        chapter = {
            "id": "chapter-1",
            "title": "Welcome",
            "subtitle": "Starter steps",
            "group_title": "Main Questline",
            "quests": [
                {"id": "q1", "title": "First Step", "hidden": False, "is_milestone": True},
                {"id": "q2", "title": "Second Step", "hidden": False, "is_milestone": False},
            ],
        }
        payload = qt.build_chapter_payload(chapter, {"completed": {"q1": {"completed_at": "2026-04-03T00:00:00Z"}}}, "0.14.1-beta")
        embed = payload["embeds"][0]
        quest_field_values = "\n".join(field["value"] for field in embed["fields"] if field["name"].startswith("Quests"))
        self.assertIn("✅ 1. First Step", quest_field_values)
        self.assertIn("⬜ 2. Second Step", quest_field_values)


class ResolveQueryTests(unittest.TestCase):
    def test_resolve_query_matches_title(self) -> None:
        quest_index = {
            "abc": {"id": "abc", "title": "Welcome to All the Mons!", "subtitle": "", "chapter_title": "Welcome"},
            "def": {"id": "def", "title": "Another Quest", "subtitle": "", "chapter_title": "Elsewhere"},
        }
        matches = qt.resolve_query("all the mons", quest_index)
        self.assertEqual([row["id"] for row in matches], ["abc"])


if __name__ == "__main__":
    unittest.main()
