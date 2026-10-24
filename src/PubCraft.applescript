-- PubCraft Launcher per macOS
-- Avvia pubcraftgui incorporato nell'App bundle e apre l'interfaccia nel browser.

on run
	set myPath to POSIX path of (path to me)
	set binPath to myPath & "Contents/MacOS/pubcraftgui"
	try
		do shell script quoted form of binPath & " >/dev/null 2>&1 &"
	on error errMsg
		display dialog "Errore durante l'avvio di PubCraft: " & errMsg buttons {"OK"} default button 1 with icon stop
	end try
end run

on open droppedItems
	repeat with anItem in droppedItems
		set p to POSIX path of anItem
		set myPath to POSIX path of (path to me)
		set binPath to myPath & "Contents/MacOS/pubcraftgui"
		try
			do shell script quoted form of binPath & " " & quoted form of p & " >/dev/null 2>&1 &"
		on error errMsg
			display dialog "Errore durante l'apertura in PubCraft: " & errMsg buttons {"OK"} default button 1 with icon stop
		end try
		exit repeat
	end repeat
end open
