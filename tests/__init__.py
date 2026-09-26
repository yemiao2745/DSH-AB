"""Test package.

The toolchain interpreter is an embeddable distribution whose python312._pth keeps the working
directory off sys.path, and unittest's discovery refuses a start directory that is not importable
when it differs from the top-level directory. This empty package marker is what makes

    tools\python\python.exe -X utf8 -m unittest discover -s tests -t . -v

work from the project root.
"""
