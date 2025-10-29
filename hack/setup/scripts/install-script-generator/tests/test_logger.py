#!/usr/bin/env python3

# Copyright 2025 The KServe Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Tests for logger module."""

import sys
from pathlib import Path
from io import StringIO

# Add parent directory to path for imports
sys.path.insert(0, str(Path(__file__).parent.parent))

from pkg import logger


def test_log_info(capsys):
    """Test log_info outputs to stdout with correct format."""
    logger.log_info("Test message")
    captured = capsys.readouterr()
    assert "[INFO]" in captured.out
    assert "Test message" in captured.out


def test_log_success(capsys):
    """Test log_success outputs to stdout with correct format."""
    logger.log_success("Success message")
    captured = capsys.readouterr()
    assert "[SUCCESS]" in captured.out
    assert "Success message" in captured.out


def test_log_error(capsys):
    """Test log_error outputs to stderr with correct format."""
    logger.log_error("Error message")
    captured = capsys.readouterr()
    assert "[ERROR]" in captured.err
    assert "Error message" in captured.err


def test_colors_defined():
    """Test that color constants are defined."""
    assert hasattr(logger.Colors, 'BLUE')
    assert hasattr(logger.Colors, 'GREEN')
    assert hasattr(logger.Colors, 'RED')
    assert hasattr(logger.Colors, 'RESET')
    assert isinstance(logger.Colors.BLUE, str)
    assert isinstance(logger.Colors.GREEN, str)
    assert isinstance(logger.Colors.RED, str)
    assert isinstance(logger.Colors.RESET, str)
