// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

// Package claims hält den Claim-Kontrakt fest: was jede App über eine
// angemeldete Person erfährt, egal welcher Anmeldedienst dahintersteht.
//
//	sub     stabile ID der Person, eindeutig in dieser Installation
//	name    Anzeigename, immer gesetzt
//	groups  enthält die Rolle der Person
//	email   nur, wenn der Anmeldedienst eine liefert — nicht garantiert,
//	        Schüler:innen haben oft keine
//
// Welche groups-Werte Lehrkraft und Schüler:in bedeuten, legt stackctl fest
// und gibt es den Apps über die Umgebung mit (TeacherGroupsEnv,
// StudentGroupsEnv). Apps vergleichen nie gegen fest eingetragene Werte eines
// Anbieters: ein Anbieterwechsel ändert dann nur die Zuordnung, keine App.
package claims

// Die Rollen, die eine App unterscheiden kann.
const (
	RoleTeacher = "lehrkraft"
	RoleStudent = "schueler"
)

// Umgebungsvariablen, über die stackctl jeder App sagt, welche groups-Werte
// welche Rolle bedeuten. Mehrere Werte stehen kommagetrennt.
const (
	TeacherGroupsEnv = "ROLE_TEACHER_GROUPS"
	StudentGroupsEnv = "ROLE_STUDENT_GROUPS"
)

// Roles lists every role in display order.
func Roles() []string { return []string{RoleTeacher, RoleStudent} }

// ValidRole reports whether r is one of the contract's roles.
func ValidRole(r string) bool {
	for _, known := range Roles() {
		if r == known {
			return true
		}
	}
	return false
}

// RoleLabel is how a role reads in the admin UI.
func RoleLabel(r string) string {
	switch r {
	case RoleTeacher:
		return "Lehrkraft"
	case RoleStudent:
		return "Schüler:in"
	default:
		return r
	}
}

// TeacherGroups and StudentGroups are the groups values that carry each role.
// With Dex's own test accounts the value is the role itself; once a school's
// sign-in service is connected, its own values take their place here.
func TeacherGroups() string { return RoleTeacher }
func StudentGroups() string { return RoleStudent }
