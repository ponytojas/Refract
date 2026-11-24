# dmgbuild settings for the Refract disk image (run through scripts/make-dmg.py, which passes the
# app in as -Dapp=...). dmgbuild writes the window's .DS_Store itself: no Finder, no AppleScript,
# so it works on a headless runner. Positions are in points, from the top left of the window's content.
import os

here = os.path.dirname(os.path.abspath(defines["settings_path"] if "settings_path" in defines else __file__))  # noqa: F821
app = defines["app"]  # noqa: F821

format = "UDZO"
size = None  # let dmgbuild size the image to its contents
files = [app]
symlinks = {"Applications": "/Applications"}
icon = os.path.join(here, "..", "Resources", "AppIcon.icns")  # the volume's icon
background = os.path.join(here, "background.png")  # dmgbuild adds background@2x.png beside it

# 660 x 420 of content plus the 32 pt title bar Finder draws (a user's tab bar takes more).
window_rect = ((200, 120), (660, 452))
default_view = "icon-view"
icon_size = 128
text_size = 13
icon_locations = {os.path.basename(app): (180, 200), "Applications": (480, 200)}

show_toolbar = False
show_sidebar = False
show_pathbar = False
show_status_bar = False
show_tab_view = False
show_icon_preview = False
include_icon_view_settings = True
include_list_view_settings = False
arrange_by = None
label_pos = "bottom"
